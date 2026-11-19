package identity

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/database"
	"github.com/easycloud/easycloud/internal/pathguard"
)

// repo is the SQL persistence for identity; both dialects share these statements.
type repo struct{ db *database.DB }

const userCols = "id, username, display_name, password_hash, role, disabled, quota_bytes, created_at"

func scanUser(s interface{ Scan(...any) error }) (User, error) {
	var u User
	var created int64
	err := s.Scan(&u.ID, &u.Username, &u.DisplayName, &u.hash, &u.Role, &u.Disabled, &u.QuotaBytes, &created)
	u.CreatedAt = time.Unix(created, 0).UTC()
	return u, err
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func isDuplicate(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "unique") || strings.Contains(m, "duplicate")
}

func (r repo) userBy(ctx context.Context, col, v string) (User, error) {
	u, err := scanUser(r.db.R.QueryRowContext(ctx, "SELECT "+userCols+" FROM easycloud_users WHERE "+col+" = ?", v))
	if err != nil {
		return u, notFound(err)
	}
	u.GroupIDs, err = r.groupsOf(ctx, u.ID)
	return u, err
}

func (r repo) listUsers(ctx context.Context) ([]User, error) {
	rows, err := r.db.R.QueryContext(ctx, "SELECT "+userCols+" FROM easycloud_users ORDER BY username")
	if err != nil {
		return nil, err
	}
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	members, err := r.allMemberships(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].GroupIDs = members[out[i].ID]
		if out[i].GroupIDs == nil {
			out[i].GroupIDs = []string{}
		}
	}
	return out, nil
}

func (r repo) insertUser(ctx context.Context, tx *sql.Tx, u User) error {
	now := time.Now().Unix()
	_, err := tx.ExecContext(ctx, "INSERT INTO easycloud_users ("+userCols+", updated_at) VALUES (?,?,?,?,?,?,?,?,?)",
		u.ID, u.Username, u.DisplayName, u.hash, u.Role, u.Disabled, u.QuotaBytes, now, now)
	if isDuplicate(err) {
		return ErrDuplicate
	}
	return err
}

func (r repo) countActiveAdmins(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM easycloud_users WHERE role = 'admin' AND disabled = 0").Scan(&n)
	return n, err
}

func (r repo) groupsOf(ctx context.Context, userID string) ([]string, error) {
	rows, err := r.db.R.QueryContext(ctx, "SELECT group_id FROM easycloud_group_members WHERE user_id = ? ORDER BY group_id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r repo) allMemberships(ctx context.Context) (map[string][]string, error) {
	rows, err := r.db.R.QueryContext(ctx, "SELECT group_id, user_id FROM easycloud_group_members ORDER BY group_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var g, u string
		if err := rows.Scan(&g, &u); err != nil {
			return nil, err
		}
		out[u] = append(out[u], g)
	}
	return out, rows.Err()
}

func (r repo) listGroups(ctx context.Context) ([]Group, error) {
	rows, err := r.db.R.QueryContext(ctx, "SELECT id, name, parent_id, created_at FROM easycloud_groups ORDER BY name")
	if err != nil {
		return nil, err
	}
	var out []Group
	idx := map[string]int{}
	for rows.Next() {
		var g Group
		var parent sql.NullString
		var created int64
		if err := rows.Scan(&g.ID, &g.Name, &parent, &created); err != nil {
			rows.Close()
			return nil, err
		}
		g.ParentID, g.CreatedAt, g.MemberIDs = parent.String, time.Unix(created, 0).UTC(), []string{}
		idx[g.ID] = len(out)
		out = append(out, g)
	}
	rows.Close()
	members, err := r.allMemberships(ctx)
	if err != nil {
		return nil, err
	}
	for u, gs := range members {
		for _, g := range gs {
			if i, ok := idx[g]; ok {
				out[i].MemberIDs = append(out[i].MemberIDs, u)
			}
		}
	}
	return out, nil
}

const grantCols = "id, subject_type, subject_id, path, perm, created_by, created_at"

func scanGrant(s interface{ Scan(...any) error }) (Grant, error) {
	var g Grant
	var created int64
	var perm int
	err := s.Scan(&g.ID, &g.SubjectType, &g.SubjectID, &g.Path, &perm, &g.CreatedBy, &created)
	g.Perm = pathguard.Perm(perm)
	g.PermName = g.Perm.String()
	g.CreatedAt = time.Unix(created, 0).UTC()
	return g, err
}

func (r repo) listGrants(ctx context.Context, where string, args ...any) ([]Grant, error) {
	q := "SELECT " + grantCols + " FROM easycloud_grants"
	if where != "" {
		q += " WHERE " + where
	}
	rows, err := r.db.R.QueryContext(ctx, q+" ORDER BY path", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
