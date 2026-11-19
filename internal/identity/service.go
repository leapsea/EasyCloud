package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/pathguard"
	"golang.org/x/crypto/bcrypt"
)

const SessionTTL = 30 * 24 * time.Hour

type Service struct {
	db       *database.DB
	r        repo
	dataRoot *os.Root
	log      *slog.Logger

	cacheMu sync.Mutex
	cache   map[string]cachedGrants // userID -> resolved grants
	limiter limiter

	listenMu  sync.Mutex
	listeners []func() // told when users, departments, grants or app passwords change

	dummyHash []byte // compared against when the user does not exist (constant-ish timing)
}

type cachedGrants struct {
	grants []pathguard.Grant
	at     time.Time
}

func NewService(db *database.DB, dataRoot *os.Root, log *slog.Logger) *Service {
	h, _ := bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), bcrypt.DefaultCost)
	return &Service{db: db, r: repo{db: db}, dataRoot: dataRoot, log: log,
		cache: map[string]cachedGrants{}, limiter: limiter{hits: map[string][]time.Time{}}, dummyHash: h}
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func (s *Service) invalidate() {
	s.cacheMu.Lock()
	s.cache = map[string]cachedGrants{}
	s.cacheMu.Unlock()
	s.notify()
}

// OnChange registers a callback for identity changes (e.g. to resync Samba).
func (s *Service) OnChange(fn func()) {
	s.listenMu.Lock()
	s.listeners = append(s.listeners, fn)
	s.listenMu.Unlock()
}

func (s *Service) notify() {
	s.listenMu.Lock()
	ls := append([]func(){}, s.listeners...)
	s.listenMu.Unlock()
	for _, fn := range ls {
		fn()
	}
}

func (s *Service) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- bootstrap ----------

// EnsureAdmin creates the first administrator when no user exists. The
// configured password is used only then; later resets go through
// `easycloud reset-password` so Dashboard password changes are not undone on restart.
func (s *Service) EnsureAdmin(ctx context.Context, configured string) error {
	var n int
	if err := s.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM easycloud_users").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	pw := configured
	if pw == "" {
		pw = randomToken(12)
		s.log.Warn("generated administrator password (username: admin); set EASYCLOUD_ADMIN_PASSWORD before first start to choose your own", "password", pw)
	}
	_, err := s.CreateUser(ctx, "", CreateUserInput{ID: "admin", Username: "admin", DisplayName: "管理员", Password: pw, Role: RoleAdmin})
	return err
}

// ---------- users ----------

type CreateUserInput struct {
	ID          string   `json:"-"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Password    string   `json:"password"`
	Role        string   `json:"role"`
	QuotaBytes  int64    `json:"quotaBytes"`
	GroupIDs    []string `json:"groupIds"`
}

func validPassword(p string) bool { return len(p) >= 8 && len(p) <= 128 }

// CreateUser creates the account, its personal directory user/<name>, and the rw grant on it.
func (s *Service) CreateUser(ctx context.Context, by string, in CreateUserInput) (User, error) {
	in.Username = strings.TrimSpace(in.Username)
	if !ValidUsername(in.Username) {
		return User{}, fmt.Errorf("%w: username must match [a-z0-9][a-z0-9_.-]{0,31}", ErrInvalid)
	}
	if !validPassword(in.Password) {
		return User{}, fmt.Errorf("%w: password must be 8-128 characters", ErrInvalid)
	}
	if in.Role == "" {
		in.Role = RoleUser
	}
	if in.Role != RoleUser && in.Role != RoleAdmin {
		return User{}, fmt.Errorf("%w: role", ErrInvalid)
	}
	if in.QuotaBytes < 0 {
		return User{}, fmt.Errorf("%w: quota", ErrInvalid)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	u := User{ID: in.ID, Username: in.Username, DisplayName: strings.TrimSpace(in.DisplayName), Role: in.Role,
		QuotaBytes: in.QuotaBytes, hash: string(hash)}
	if u.ID == "" {
		u.ID = newID()
	}
	if u.DisplayName == "" {
		u.DisplayName = u.Username
	}
	home := u.Home()
	if err := s.dataRoot.MkdirAll(home, 0o755); err != nil {
		return User{}, fmt.Errorf("create home %s: %w", home, err)
	}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if err := s.r.insertUser(ctx, tx, u); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO easycloud_grants ("+grantCols+") VALUES (?,?,?,?,?,?,?)",
			newID(), SubjectUser, u.ID, "/"+home, int(pathguard.PermRW), by, time.Now().Unix()); err != nil {
			return err
		}
		return setMemberships(ctx, tx, u.ID, in.GroupIDs)
	})
	if err != nil {
		return User{}, err
	}
	s.invalidate()
	return s.r.userBy(ctx, "id", u.ID)
}

func setMemberships(ctx context.Context, tx *sql.Tx, userID string, groupIDs []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM easycloud_group_members WHERE user_id = ?", userID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, g := range groupIDs {
		if seen[g] {
			continue
		}
		seen[g] = true
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM easycloud_groups WHERE id = ?", g).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: unknown group %s", ErrInvalid, g)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO easycloud_group_members (group_id, user_id) VALUES (?, ?)", g, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GetUser(ctx context.Context, id string) (User, error) {
	return s.r.userBy(ctx, "id", id)
}
func (s *Service) ListUsers(ctx context.Context) ([]User, error) { return s.r.listUsers(ctx) }

type UpdateUserInput struct {
	DisplayName *string   `json:"displayName"`
	Role        *string   `json:"role"`
	Disabled    *bool     `json:"disabled"`
	QuotaBytes  *int64    `json:"quotaBytes"`
	GroupIDs    *[]string `json:"groupIds"`
}

func (s *Service) UpdateUser(ctx context.Context, actor, id string, in UpdateUserInput) (User, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		u, err := scanUser(tx.QueryRowContext(ctx, "SELECT "+userCols+" FROM easycloud_users WHERE id = ?", id))
		if err != nil {
			return notFound(err)
		}
		wasActiveAdmin := u.Role == RoleAdmin && !u.Disabled
		if in.DisplayName != nil {
			u.DisplayName = strings.TrimSpace(*in.DisplayName)
		}
		if in.Role != nil {
			if *in.Role != RoleUser && *in.Role != RoleAdmin {
				return fmt.Errorf("%w: role", ErrInvalid)
			}
			u.Role = *in.Role
		}
		if in.Disabled != nil {
			if *in.Disabled && id == actor {
				return fmt.Errorf("%w: you cannot disable yourself", ErrInvalid)
			}
			u.Disabled = *in.Disabled
		}
		if in.QuotaBytes != nil {
			if *in.QuotaBytes < 0 {
				return fmt.Errorf("%w: quota", ErrInvalid)
			}
			u.QuotaBytes = *in.QuotaBytes
		}
		if _, err := tx.ExecContext(ctx, "UPDATE easycloud_users SET display_name = ?, role = ?, disabled = ?, quota_bytes = ?, updated_at = ? WHERE id = ?",
			u.DisplayName, u.Role, u.Disabled, u.QuotaBytes, time.Now().Unix(), id); err != nil {
			return err
		}
		if wasActiveAdmin && (u.Role != RoleAdmin || u.Disabled) {
			if n, err := s.r.countActiveAdmins(ctx, tx); err != nil {
				return err
			} else if n == 0 {
				return ErrLastAdmin
			}
		}
		if u.Disabled {
			if _, err := tx.ExecContext(ctx, "DELETE FROM easycloud_sessions WHERE user_id = ?", id); err != nil {
				return err
			}
		}
		if in.GroupIDs != nil {
			return setMemberships(ctx, tx, id, *in.GroupIDs)
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	s.invalidate()
	return s.r.userBy(ctx, "id", id)
}

// DeleteUser removes the account, its memberships, grants and sessions. Files
// (including the personal directory) are kept for the administrator to handle.
func (s *Service) DeleteUser(ctx context.Context, actor, id string) error {
	if id == actor {
		return fmt.Errorf("%w: you cannot delete yourself", ErrInvalid)
	}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM easycloud_users WHERE id = ?", id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		if n, err := s.r.countActiveAdmins(ctx, tx); err != nil {
			return err
		} else if n == 0 {
			return ErrLastAdmin
		}
		for _, q := range []string{
			"DELETE FROM easycloud_app_passwords WHERE user_id = ?",
			"DELETE FROM easycloud_group_members WHERE user_id = ?",
			"DELETE FROM easycloud_grants WHERE subject_type = 'user' AND subject_id = ?",
			"DELETE FROM easycloud_sessions WHERE user_id = ?",
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.invalidate()
	}
	return err
}

// SetPassword replaces a password and ends that user's other sessions.
func (s *Service) SetPassword(ctx context.Context, id, password, keepToken string) error {
	if !validPassword(password) {
		return fmt.Errorf("%w: password must be 8-128 characters", ErrInvalid)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE easycloud_users SET password_hash = ?, updated_at = ? WHERE id = ?", string(hash), time.Now().Unix(), id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM easycloud_sessions WHERE user_id = ? AND token_hash <> ?", id, hashToken(keepToken))
		return err
	})
}

// ChangeOwnPassword verifies the current password first.
func (s *Service) ChangeOwnPassword(ctx context.Context, id, current, next, keepToken string) error {
	u, err := s.r.userBy(ctx, "id", id)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.hash), []byte(current)) != nil {
		return ErrBadCredentials
	}
	return s.SetPassword(ctx, id, next, keepToken)
}

// ---------- sessions ----------

func (s *Service) Login(ctx context.Context, clientIP, username, password string) (User, string, error) {
	key := clientIP + "|" + strings.ToLower(username)
	if s.limiter.limited(clientIP) || s.limiter.limited(key) {
		return User{}, "", ErrRateLimited
	}
	u, err := s.r.userBy(ctx, "username", strings.TrimSpace(strings.ToLower(username)))
	if errors.Is(err, ErrNotFound) {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		s.limiter.record(clientIP, key)
		return User{}, "", ErrBadCredentials
	} else if err != nil {
		return User{}, "", err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.hash), []byte(password)) != nil || u.Disabled {
		s.limiter.record(clientIP, key)
		return User{}, "", ErrBadCredentials
	}
	tok := randomToken(32)
	_, err = s.db.W.ExecContext(ctx, "INSERT INTO easycloud_sessions (token_hash, expires_at, user_id) VALUES (?, ?, ?)",
		hashToken(tok), time.Now().Add(SessionTTL).Unix(), u.ID)
	return u, tok, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.db.W.ExecContext(ctx, "DELETE FROM easycloud_sessions WHERE token_hash = ?", hashToken(token))
	return err
}

// Authenticate resolves a session token; disabled users are rejected.
func (s *Service) Authenticate(ctx context.Context, token string) (User, bool) {
	if token == "" {
		return User{}, false
	}
	var uid string
	var exp int64
	err := s.db.R.QueryRowContext(ctx, "SELECT user_id, expires_at FROM easycloud_sessions WHERE token_hash = ?", hashToken(token)).Scan(&uid, &exp)
	if err != nil || time.Now().Unix() > exp {
		return User{}, false
	}
	u, err := scanUser(s.db.R.QueryRowContext(ctx, "SELECT "+userCols+" FROM easycloud_users WHERE id = ?", uid))
	if err != nil || u.Disabled {
		return User{}, false
	}
	return u, true
}

func (s *Service) PurgeExpiredSessions(ctx context.Context) {
	if _, err := s.db.W.ExecContext(ctx, "DELETE FROM easycloud_sessions WHERE expires_at < ?", time.Now().Unix()); err != nil {
		s.log.Error("purge sessions", "err", err)
	}
}

// ---------- groups (departments) ----------

func (s *Service) ListGroups(ctx context.Context) ([]Group, error) { return s.r.listGroups(ctx) }

func (s *Service) CreateGroup(ctx context.Context, name, parentID string) (Group, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 {
		return Group{}, fmt.Errorf("%w: name", ErrInvalid)
	}
	id := newID()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var parent any
		if parentID != "" {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM easycloud_groups WHERE id = ?", parentID).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("%w: unknown parent", ErrInvalid)
			}
			parent = parentID
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO easycloud_groups (id, name, parent_id, created_at) VALUES (?,?,?,?)", id, name, parent, time.Now().Unix())
		return err
	})
	if err != nil {
		return Group{}, err
	}
	return Group{ID: id, Name: name, ParentID: parentID, MemberIDs: []string{}, CreatedAt: time.Now().UTC()}, nil
}

type UpdateGroupInput struct {
	Name      *string   `json:"name"`
	ParentID  *string   `json:"parentId"`
	MemberIDs *[]string `json:"memberIds"`
}

func (s *Service) UpdateGroup(ctx context.Context, id string, in UpdateGroupInput) error {
	groups, err := s.r.listGroups(ctx)
	if err != nil {
		return err
	}
	parentOf := map[string]string{}
	found := false
	for _, g := range groups {
		parentOf[g.ID] = g.ParentID
		found = found || g.ID == id
	}
	if !found {
		return ErrNotFound
	}
	err = s.tx(ctx, func(tx *sql.Tx) error {
		if in.Name != nil {
			n := strings.TrimSpace(*in.Name)
			if n == "" || len(n) > 128 {
				return fmt.Errorf("%w: name", ErrInvalid)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE easycloud_groups SET name = ? WHERE id = ?", n, id); err != nil {
				return err
			}
		}
		if in.ParentID != nil {
			p := *in.ParentID
			if p != "" {
				if _, ok := parentOf[p]; !ok {
					return fmt.Errorf("%w: unknown parent", ErrInvalid)
				}
				for cur := p; cur != ""; cur = parentOf[cur] { // cycle check
					if cur == id {
						return fmt.Errorf("%w: a department cannot be moved under itself", ErrInvalid)
					}
				}
			}
			var parent any
			if p != "" {
				parent = p
			}
			if _, err := tx.ExecContext(ctx, "UPDATE easycloud_groups SET parent_id = ? WHERE id = ?", parent, id); err != nil {
				return err
			}
		}
		if in.MemberIDs != nil {
			if _, err := tx.ExecContext(ctx, "DELETE FROM easycloud_group_members WHERE group_id = ?", id); err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, u := range *in.MemberIDs {
				if seen[u] {
					continue
				}
				seen[u] = true
				if _, err := tx.ExecContext(ctx, "INSERT INTO easycloud_group_members (group_id, user_id) SELECT ?, id FROM easycloud_users WHERE id = ?", id, u); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		s.invalidate()
	}
	return err
}

// DeleteGroup re-parents children to the deleted group's parent and drops its members and grants.
func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var parent sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT parent_id FROM easycloud_groups WHERE id = ?", id).Scan(&parent); err != nil {
			return notFound(err)
		}
		var np any
		if parent.Valid {
			np = parent.String
		}
		stmts := []struct {
			q    string
			args []any
		}{
			{"UPDATE easycloud_groups SET parent_id = ? WHERE parent_id = ?", []any{np, id}},
			{"DELETE FROM easycloud_group_members WHERE group_id = ?", []any{id}},
			{"DELETE FROM easycloud_grants WHERE subject_type = 'group' AND subject_id = ?", []any{id}},
			{"DELETE FROM easycloud_groups WHERE id = ?", []any{id}},
		}
		for _, st := range stmts {
			if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.invalidate()
	}
	return err
}

// ---------- grants ----------

func (s *Service) ListGrants(ctx context.Context, subjectType, subjectID string) ([]Grant, error) {
	if subjectType != "" {
		return s.r.listGrants(ctx, "subject_type = ? AND subject_id = ?", subjectType, subjectID)
	}
	return s.r.listGrants(ctx, "")
}

func (s *Service) CreateGrant(ctx context.Context, by, subjectType, subjectID, rawPath, perm string) (Grant, error) {
	p, err := pathguard.Parse(rawPath)
	if err != nil {
		return Grant{}, fmt.Errorf("%w: path", ErrInvalid)
	}
	var pm pathguard.Perm
	switch perm {
	case "r":
		pm = pathguard.PermRead
	case "rw":
		pm = pathguard.PermRW
	default:
		return Grant{}, fmt.Errorf("%w: perm must be r or rw", ErrInvalid)
	}
	table := map[string]string{SubjectUser: "easycloud_users", SubjectGroup: "easycloud_groups"}[subjectType]
	if table == "" {
		return Grant{}, fmt.Errorf("%w: subjectType", ErrInvalid)
	}
	var n int
	if err := s.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id = ?", subjectID).Scan(&n); err != nil {
		return Grant{}, err
	}
	if n == 0 {
		return Grant{}, fmt.Errorf("%w: unknown subject", ErrInvalid)
	}
	if fi, err := s.dataRoot.Stat(p.String()); err != nil || !fi.IsDir() {
		return Grant{}, fmt.Errorf("%w: %s is not an existing directory", ErrInvalid, p.Display())
	}
	g := Grant{ID: newID(), SubjectType: subjectType, SubjectID: subjectID, Path: p.Display(), Perm: pm, PermName: pm.String(), CreatedBy: by, CreatedAt: time.Now().UTC()}
	q := s.db.Upsert("easycloud_grants", []string{"id", "subject_type", "subject_id", "path", "perm", "created_by", "created_at"}, []string{"subject_type", "subject_id", "path"})
	if s.db.Dialect == database.MySQL {
		q = strings.Replace(q, "id = VALUES(id), ", "", 1) // keep the existing id on update
	} else {
		q = strings.Replace(q, "id = excluded.id, ", "", 1)
	}
	if _, err := s.db.W.ExecContext(ctx, q, g.ID, g.SubjectType, g.SubjectID, g.Path, int(g.Perm), g.CreatedBy, g.CreatedAt.Unix()); err != nil {
		return Grant{}, err
	}
	s.invalidate()
	gs, err := s.r.listGrants(ctx, "subject_type = ? AND subject_id = ? AND path = ?", subjectType, subjectID, g.Path)
	if err != nil || len(gs) == 0 {
		return g, err
	}
	return gs[0], nil
}

func (s *Service) DeleteGrant(ctx context.Context, id string) error {
	res, err := s.db.W.ExecContext(ctx, "DELETE FROM easycloud_grants WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.invalidate()
	return nil
}

// GrantsFor implements pathguard.GrantSource: the user's own grants plus those
// of every department they belong to and every ancestor department.
func (s *Service) GrantsFor(ctx context.Context, userID string) ([]pathguard.Grant, error) {
	s.cacheMu.Lock()
	if c, ok := s.cache[userID]; ok && time.Since(c.at) < time.Minute {
		s.cacheMu.Unlock()
		return c.grants, nil
	}
	s.cacheMu.Unlock()

	groups, err := s.r.listGroups(ctx)
	if err != nil {
		return nil, err
	}
	parentOf := map[string]string{}
	for _, g := range groups {
		parentOf[g.ID] = g.ParentID
	}
	direct, err := s.r.groupsOf(ctx, userID)
	if err != nil {
		return nil, err
	}
	all := map[string]bool{}
	for _, g := range direct {
		for cur, depth := g, 0; cur != "" && !all[cur] && depth < 64; cur, depth = parentOf[cur], depth+1 {
			all[cur] = true
		}
	}
	where := "(subject_type = 'user' AND subject_id = ?)"
	args := []any{userID}
	if len(all) > 0 {
		where += " OR (subject_type = 'group' AND subject_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(all)), ",") + "))"
		for g := range all {
			args = append(args, g)
		}
	}
	rows, err := s.r.listGrants(ctx, where, args...)
	if err != nil {
		return nil, err
	}
	out := make([]pathguard.Grant, 0, len(rows))
	for _, g := range rows {
		p, err := pathguard.Parse(g.Path)
		if err != nil {
			continue
		}
		out = append(out, pathguard.Grant{Path: p, Perm: g.Perm})
	}
	s.cacheMu.Lock()
	s.cache[userID] = cachedGrants{grants: out, at: time.Now()}
	s.cacheMu.Unlock()
	return out, nil
}

// PathMoved rewrites grants when a directory is renamed or moved through the API.
func (s *Service) PathMoved(ctx context.Context, from, to pathguard.RelPath) {
	gs, err := s.r.listGrants(ctx, "")
	if err != nil {
		s.log.Error("grant path rewrite", "err", err)
		return
	}
	changed := false
	for _, g := range gs {
		p, err := pathguard.Parse(g.Path)
		if err != nil || !p.HasPrefix(from) {
			continue
		}
		np := to
		for _, seg := range p.Segments()[len(from.Segments()):] {
			np, _ = np.Join(seg)
		}
		if _, err := s.db.W.ExecContext(ctx, "UPDATE easycloud_grants SET path = ? WHERE id = ?", np.Display(), g.ID); err != nil {
			s.log.Error("grant path rewrite", "id", g.ID, "err", err)
			continue
		}
		changed = true
	}
	if changed {
		s.invalidate()
	}
}

// ---------- login rate limiting ----------

type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

const (
	maxFailures   = 5
	failureWindow = time.Minute
)

func (l *limiter) limited(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-failureWindow)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
		return false
	}
	l.hits[key] = kept
	// Per-IP limit is looser than per-account so one NAT does not lock out everyone.
	limit := maxFailures
	if !strings.Contains(key, "|") {
		limit = maxFailures * 4
	}
	return len(kept) >= limit
}

func (l *limiter) record(keys ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		l.hits[k] = append(l.hits[k], time.Now())
	}
}
