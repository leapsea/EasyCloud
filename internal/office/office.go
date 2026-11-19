// Package office connects to an OnlyOffice-compatible Document Server. The
// connector plugin supplies the server URL and JWT secret; this package issues
// editor configurations and handles the save-back callback.
package office

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/filetoken"
	"github.com/easycloud/easycloud/internal/identity"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	storageapp "github.com/easycloud/easycloud/internal/storage/app"
	"github.com/go-chi/chi/v5"
)

const ConnectorKind = "onlyoffice"

// ConnectorSource returns the active connector configuration, if any.
type ConnectorSource interface {
	Connector(kind string) (map[string]any, bool)
}

type Service struct {
	conn   ConnectorSource
	files  *storageapp.FileService
	ids    *identity.Service
	signer *filetoken.Signer
	audit  *audit.Recorder
	client *http.Client
	log    *slog.Logger
}

func New(conn ConnectorSource, files *storageapp.FileService, ids *identity.Service, signer *filetoken.Signer, rec *audit.Recorder, log *slog.Logger) *Service {
	return &Service{conn: conn, files: files, ids: ids, signer: signer, audit: rec, log: log,
		client: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type settings struct {
	server    *url.URL
	secret    string
	publicURL string
}

func (s *Service) settings() (settings, error) {
	cfg, ok := s.conn.Connector(ConnectorKind)
	if !ok {
		return settings{}, errs.New(http.StatusNotFound, "OFFICE_NOT_CONFIGURED").
			WithAction(errs.Action{Key: "officeConfigure", Label: "Configure the OnlyOffice connector", Route: "/admin/plugins", Role: "admin"})
	}
	str := func(k string) string { v, _ := cfg[k].(string); return strings.TrimSpace(v) }
	u, err := url.Parse(strings.TrimRight(str("documentServerUrl"), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || str("jwtSecret") == "" {
		return settings{}, errs.New(http.StatusServiceUnavailable, "OFFICE_MISCONFIGURED").
			WithAction(errs.Action{Key: "officeCheck", Label: "Check the OnlyOffice settings", Route: "/admin/plugins", Role: "admin"})
	}
	return settings{server: u, secret: str("jwtSecret"), publicURL: strings.TrimRight(str("publicUrl"), "/")}, nil
}

var docTypes = map[string]string{
	"doc": "word", "docx": "word", "odt": "word", "rtf": "word", "txt": "word", "docm": "word", "dotx": "word",
	"xls": "cell", "xlsx": "cell", "ods": "cell", "csv": "cell", "xlsm": "cell",
	"ppt": "slide", "pptx": "slide", "odp": "slide", "ppsx": "slide",
	"pdf": "pdf",
}

// editable formats are saved back; others open read-only.
var editable = map[string]bool{"docx": true, "xlsx": true, "pptx": true, "odt": true, "ods": true, "odp": true, "csv": true, "txt": true, "rtf": true}

func ext(name string) string { return strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")) }

// Supported reports whether the editor can open a file name.
func Supported(name string) bool { _, ok := docTypes[ext(name)]; return ok }

// ---- JWT (HS256) ----

func signJWT(secret string, claims any) (string, error) {
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	b, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(b)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(h + "." + p))
	return h + "." + p + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil)), nil
}

func verifyJWT(secret, tok string) ([]byte, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, errors.New("malformed token")
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, err
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if json.Unmarshal(hb, &hdr) != nil || hdr.Alg != "HS256" {
		return nil, errors.New("unsupported JWT algorithm")
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return nil, errors.New("bad signature")
	}
	return base64.RawURLEncoding.DecodeString(parts[1])
}

// ---- HTTP ----

func (s *Service) Mount(r chi.Router, log *slog.Logger) {
	r.Post("/office/session", httpx.Handler(s.session).With(log))
}

// MountPublic registers the Document Server callback (no session; token + JWT).
func (s *Service) MountPublic(r chi.Router, log *slog.Logger) {
	r.Post("/office/callback/{token}", s.callback)
}

func baseURL(r *http.Request, configured string) string {
	if configured != "" {
		return configured
	}
	scheme := "http"
	if httpx.IsHTTPS(r) {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func (s *Service) session(w http.ResponseWriter, r *http.Request) error {
	st, err := s.settings()
	if err != nil {
		return err
	}
	var in struct {
		Path string `json:"path"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	sub := httpx.Subject(r)
	e, err := s.files.Stat(r.Context(), sub, in.Path)
	if err != nil {
		return err
	}
	dt, ok := docTypes[ext(e.Name)]
	if e.IsDir || !ok {
		return errs.ErrInvalidInput.WithDetail("unsupported document type")
	}
	p, _ := pathguard.Parse(e.Path)
	canWrite := false
	if parent, err := s.files.List(r.Context(), sub, p.Parent().Display()); err == nil {
		canWrite = parent.Access == "rw"
	}
	mode := "view"
	if canWrite && editable[ext(e.Name)] {
		mode = "edit"
	}
	base := baseURL(r, st.publicURL)
	raw := s.signer.Issue(filetoken.Claims{UserID: sub.UserID, Path: e.Path, Purpose: "raw"}, 8*time.Hour)
	cb := s.signer.Issue(filetoken.Claims{UserID: sub.UserID, Path: e.Path, Purpose: "office-cb", Write: mode == "edit"}, 48*time.Hour)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", e.Path, e.ModTime.UnixNano(), e.Size)))
	u, _ := s.ids.GetUser(r.Context(), sub.UserID)

	cfg := map[string]any{
		"document": map[string]any{
			"fileType": ext(e.Name), "key": hex.EncodeToString(sum[:])[:20], "title": e.Name,
			"url":         base + "/api/raw/" + raw + "/" + url.PathEscape(e.Name),
			"permissions": map[string]any{"edit": mode == "edit", "download": true, "print": true},
		},
		"documentType": dt,
		"editorConfig": map[string]any{
			"mode": mode, "lang": "zh-CN", "callbackUrl": base + "/api/office/callback/" + cb,
			"user":          map[string]any{"id": sub.UserID, "name": u.DisplayName},
			"customization": map[string]any{"forcesave": true, "autosave": true},
		},
	}
	tok, err := signJWT(st.secret, cfg)
	if err != nil {
		return err
	}
	cfg["token"] = tok
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"server": st.server.String(), "config": cfg})
	return nil
}

type callbackBody struct {
	Key    string `json:"key"`
	Status int    `json:"status"`
	URL    string `json:"url"`
	Token  string `json:"token"`
}

func reply(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"error":%d}`, code)
}

// callback implements the Document Server save protocol. Status 2 = document
// ready for saving, 6 = force save; others only need an acknowledgement.
func (s *Service) callback(w http.ResponseWriter, r *http.Request) {
	st, err := s.settings()
	if err != nil {
		reply(w, 1)
		return
	}
	claims, err := s.signer.Verify(chi.URLParam(r, "token"), "office-cb")
	if err != nil {
		http.Error(w, "invalid token", http.StatusForbidden)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		reply(w, 1)
		return
	}
	var body callbackBody
	if err := json.Unmarshal(raw, &body); err != nil {
		reply(w, 1)
		return
	}
	// The JWT is in the body ("token") or the Authorization header (payload wrapped).
	jwt := body.Token
	if jwt == "" {
		jwt = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	payload, err := verifyJWT(st.secret, jwt)
	if err != nil {
		s.log.Warn("office callback with invalid JWT", "err", err)
		http.Error(w, "invalid jwt", http.StatusForbidden)
		return
	}
	var signed callbackBody
	var wrapped struct {
		Payload callbackBody `json:"payload"`
	}
	if json.Unmarshal(payload, &wrapped) == nil && wrapped.Payload.Status != 0 {
		signed = wrapped.Payload
	} else if err := json.Unmarshal(payload, &signed); err != nil {
		reply(w, 1)
		return
	}
	if signed.Status != 2 && signed.Status != 6 {
		reply(w, 0)
		return
	}
	if !claims.Write {
		reply(w, 0) // opened read-only: nothing to save
		return
	}
	if err := s.save(r, st, claims, signed.URL); err != nil {
		s.log.Error("office save failed", "path", claims.Path, "err", err)
		reply(w, 1)
		return
	}
	reply(w, 0)
}

func (s *Service) save(r *http.Request, st settings, c filetoken.Claims, src string) error {
	u, err := url.Parse(src)
	if err != nil {
		return err
	}
	// SSRF guard: only fetch from the configured Document Server.
	if !strings.EqualFold(u.Host, st.server.Host) || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("refusing download from %s (expected %s)", u.Host, st.server.Host)
	}
	user, err := s.ids.GetUser(r.Context(), c.UserID)
	if err != nil || user.Disabled {
		return errors.New("user no longer active")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("document server returned %d", res.StatusCode)
	}
	sub := user.Subject()
	e, err := s.files.Replace(r.Context(), sub, c.Path, io.LimitReader(res.Body, 2<<30))
	if err != nil {
		return err
	}
	s.audit.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: "office.save", Path: e.Path, Detail: fmt.Sprint(e.Size)})
	return nil
}
