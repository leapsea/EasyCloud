package identity

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/easycloud/easycloud/internal/audit"
	"github.com/easycloud/easycloud/internal/platform/errs"
	"github.com/easycloud/easycloud/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
)

const CookieName = "ec_session"

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return errs.ErrNotFound
	case errors.Is(err, ErrDuplicate):
		return errs.ErrConflict.WithDetail("username already exists")
	case errors.Is(err, ErrLastAdmin):
		return errs.New(http.StatusConflict, "LAST_ADMIN")
	case errors.Is(err, ErrBadCredentials):
		return errs.New(http.StatusUnauthorized, "BAD_CREDENTIALS")
	case errors.Is(err, ErrRateLimited):
		return errs.New(http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS")
	case errors.Is(err, ErrInvalid):
		return errs.ErrInvalidInput.WithDetail(strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
	}
	return err
}

type HTTP struct {
	svc   *Service
	audit *audit.Recorder
	log   *slog.Logger
}

func NewHTTP(svc *Service, rec *audit.Recorder, log *slog.Logger) *HTTP {
	return &HTTP{svc: svc, audit: rec, log: log}
}

func (h *HTTP) h(fn httpx.Handler) http.HandlerFunc {
	return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error { return mapErr(fn(w, r)) }).With(h.log)
}

func (h *HTTP) record(r *http.Request, action, path, detail string) {
	sub := httpx.Subject(r)
	h.audit.Record(audit.Entry{UserID: sub.UserID, Username: sub.Username, Action: action, Path: path, Detail: detail, IP: httpx.ClientIP(r)})
}

// MountPublic registers routes that do not require a session.
func (h *HTTP) MountPublic(r chi.Router) {
	r.Post("/auth/login", h.h(h.login))
	r.Post("/auth/logout", h.h(h.logout))
}

// Mount registers routes for any signed-in user.
func (h *HTTP) Mount(r chi.Router) {
	r.Get("/auth/me", h.h(h.me))
	r.Post("/auth/password", h.h(h.changePassword))
	r.Get("/auth/app-passwords", h.h(func(w http.ResponseWriter, r *http.Request) error {
		items, err := h.svc.ListAppPasswords(r.Context(), httpx.Subject(r).UserID)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
		return nil
	}))
	r.Post("/auth/app-passwords", h.h(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Name string `json:"name"`
		}
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
		ap, pw, err := h.svc.CreateAppPassword(r.Context(), httpx.Subject(r).UserID, in.Name)
		if err != nil {
			return err
		}
		h.record(r, "app_password.create", "", ap.Name)
		httpx.WriteJSON(w, http.StatusCreated, map[string]any{"item": ap, "password": pw})
		return nil
	}))
	// Authenticator app (TOTP): setup returns a pending secret, enable confirms
	// it with a code, disable needs the password or a code.
	r.Post("/auth/2fa/setup", h.h(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Password string `json:"password"`
		}
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
		secret, uri, err := h.svc.SetupTOTP(r.Context(), httpx.Subject(r).UserID, in.Password)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri})
		return nil
	}))
	r.Post("/auth/2fa/enable", h.h(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Code string `json:"code"`
		}
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
		if err := h.svc.EnableTOTP(r.Context(), httpx.Subject(r).UserID, in.Code); err != nil {
			return err
		}
		h.record(r, "2fa.enable", "", "")
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
	r.Post("/auth/2fa/disable", h.h(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if err := httpx.DecodeJSON(r, &in); err != nil {
			return err
		}
		if err := h.svc.DisableTOTP(r.Context(), httpx.Subject(r).UserID, in.Password, in.Code); err != nil {
			return err
		}
		h.record(r, "2fa.disable", "", "")
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
	r.Delete("/auth/app-passwords/{id}", h.h(func(w http.ResponseWriter, r *http.Request) error {
		if err := h.svc.DeleteAppPassword(r.Context(), httpx.Subject(r).UserID, chi.URLParam(r, "id")); err != nil {
			return err
		}
		h.record(r, "app_password.delete", "", chi.URLParam(r, "id"))
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
}

// MountAdmin registers administrator routes (the caller applies RequireAdmin).
func (h *HTTP) MountAdmin(r chi.Router) {
	r.Get("/users", h.h(h.listUsers))
	r.Post("/users", h.h(h.createUser))
	r.Patch("/users/{id}", h.h(h.updateUser))
	r.Delete("/users/{id}", h.h(h.deleteUser))
	r.Post("/users/{id}/password", h.h(h.resetPassword))
	r.Post("/users/{id}/2fa/reset", h.h(func(w http.ResponseWriter, r *http.Request) error {
		id := chi.URLParam(r, "id")
		if _, err := h.svc.GetUser(r.Context(), id); err != nil {
			return err
		}
		if err := h.svc.ResetTOTP(r.Context(), id); err != nil {
			return err
		}
		h.record(r, "2fa.reset", "", id)
		w.WriteHeader(http.StatusNoContent)
		return nil
	}))
	// Every folder a user can reach: own and department grants, with sources.
	r.Get("/users/{id}/access", h.h(func(w http.ResponseWriter, r *http.Request) error {
		id := chi.URLParam(r, "id")
		items, err := h.svc.AccessOf(r.Context(), id)
		if err != nil {
			return err
		}
		u, err := h.svc.r.userBy(r.Context(), "id", id)
		if err != nil {
			return err
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"admin": u.Role == RoleAdmin, "items": items})
		return nil
	}))
	r.Get("/groups", h.h(h.listGroups))
	r.Post("/groups", h.h(h.createGroup))
	r.Patch("/groups/{id}", h.h(h.updateGroup))
	r.Delete("/groups/{id}", h.h(h.deleteGroup))
	r.Get("/grants", h.h(h.listGrants))
	r.Post("/grants", h.h(h.createGrant))
	r.Delete("/grants/{id}", h.h(h.deleteGrant))
	r.Get("/audit", h.h(h.listAudit))
}

// Require rejects requests without a valid session cookie.
func (h *HTTP) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tok string
		if c, _ := r.Cookie(CookieName); c != nil {
			tok = c.Value
		}
		u, ok := h.svc.Authenticate(r.Context(), tok)
		if !ok {
			httpx.WriteError(w, r, h.log, errs.ErrUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(httpx.WithSubject(r.Context(), u.Subject())))
	})
}

type meResp struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	IsAdmin     bool   `json:"isAdmin"`
	Home        string `json:"home"`
	QuotaBytes  int64  `json:"quotaBytes"`
	TwoFactor   bool   `json:"twoFactor"` // an authenticator app is bound
}

func me(u User) meResp {
	return meResp{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, IsAdmin: u.Role == RoleAdmin, Home: "/" + u.Home(), QuotaBytes: u.QuotaBytes}
}

func (h *HTTP) meOf(ctx context.Context, u User) meResp {
	m := me(u)
	m.TwoFactor = h.svc.TOTPEnabled(ctx, u.ID)
	return m
}

func (h *HTTP) login(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"` // authenticator code instead of the password
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if in.Username == "" {
		in.Username = "admin" // V1 clients sent only a password
	}
	var (
		u   User
		tok string
		err error
	)
	method := "password"
	if in.Password == "" && in.Code != "" {
		method = "totp"
		u, tok, err = h.svc.LoginWithCode(r.Context(), httpx.ClientIP(r), in.Username, in.Code)
	} else {
		u, tok, err = h.svc.Login(r.Context(), httpx.ClientIP(r), in.Username, in.Password)
	}
	if err != nil {
		if errors.Is(err, ErrBadCredentials) {
			h.audit.Record(audit.Entry{Username: strings.ToLower(in.Username), Action: "login.failed", Detail: method, IP: httpx.ClientIP(r)})
		}
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: tok, Path: "/", HttpOnly: true, Secure: httpx.IsHTTPS(r),
		SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(SessionTTL),
	})
	h.audit.Record(audit.Entry{UserID: u.ID, Username: u.Username, Action: "login", Detail: method, IP: httpx.ClientIP(r)})
	httpx.WriteJSON(w, http.StatusOK, h.meOf(r.Context(), u))
	return nil
}

func (h *HTTP) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := h.svc.Logout(r.Context(), c.Value); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *HTTP) me(w http.ResponseWriter, r *http.Request) error {
	u, err := h.svc.GetUser(r.Context(), httpx.Subject(r).UserID)
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, h.meOf(r.Context(), u))
	return nil
}

func token(r *http.Request) string {
	if c, _ := r.Cookie(CookieName); c != nil {
		return c.Value
	}
	return ""
}

func (h *HTTP) changePassword(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Current string `json:"current"`
		Next    string `json:"next"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.svc.ChangeOwnPassword(r.Context(), httpx.Subject(r).UserID, in.Current, in.Next, token(r)); err != nil {
		return err
	}
	h.record(r, "password.change", "", "")
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *HTTP) listUsers(w http.ResponseWriter, r *http.Request) error {
	us, err := h.svc.ListUsers(r.Context())
	if err != nil {
		return err
	}
	if bound, err := h.svc.TOTPUsers(r.Context()); err == nil {
		for i := range us {
			us[i].TwoFactor = bound[us[i].ID]
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": us})
	return nil
}

func (h *HTTP) createUser(w http.ResponseWriter, r *http.Request) error {
	var in CreateUserInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	u, err := h.svc.CreateUser(r.Context(), httpx.Subject(r).UserID, in)
	if err != nil {
		return err
	}
	h.record(r, "user.create", "/"+u.Home(), u.Username)
	httpx.WriteJSON(w, http.StatusCreated, u)
	return nil
}

func (h *HTTP) updateUser(w http.ResponseWriter, r *http.Request) error {
	var in UpdateUserInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	u, err := h.svc.UpdateUser(r.Context(), httpx.Subject(r).UserID, chi.URLParam(r, "id"), in)
	if err != nil {
		return err
	}
	h.record(r, "user.update", "", u.Username)
	httpx.WriteJSON(w, http.StatusOK, u)
	return nil
}

func (h *HTTP) deleteUser(w http.ResponseWriter, r *http.Request) error {
	id := chi.URLParam(r, "id")
	u, err := h.svc.GetUser(r.Context(), id)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteUser(r.Context(), httpx.Subject(r).UserID, id); err != nil {
		return err
	}
	h.record(r, "user.delete", "", u.Username)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *HTTP) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Password string `json:"password"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	keep := ""
	if chi.URLParam(r, "id") == httpx.Subject(r).UserID {
		keep = token(r)
	}
	if err := h.svc.SetPassword(r.Context(), chi.URLParam(r, "id"), in.Password, keep); err != nil {
		return err
	}
	h.record(r, "user.password_reset", "", chi.URLParam(r, "id"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *HTTP) listGroups(w http.ResponseWriter, r *http.Request) error {
	gs, err := h.svc.ListGroups(r.Context())
	if err != nil {
		return err
	}
	if gs == nil {
		gs = []Group{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": gs})
	return nil
}

func (h *HTTP) createGroup(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name     string `json:"name"`
		ParentID string `json:"parentId"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	g, err := h.svc.CreateGroup(r.Context(), in.Name, in.ParentID)
	if err != nil {
		return err
	}
	h.record(r, "group.create", "", g.Name)
	httpx.WriteJSON(w, http.StatusCreated, g)
	return nil
}

func (h *HTTP) updateGroup(w http.ResponseWriter, r *http.Request) error {
	var in UpdateGroupInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	if err := h.svc.UpdateGroup(r.Context(), chi.URLParam(r, "id"), in); err != nil {
		return err
	}
	h.record(r, "group.update", "", chi.URLParam(r, "id"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *HTTP) deleteGroup(w http.ResponseWriter, r *http.Request) error {
	if err := h.svc.DeleteGroup(r.Context(), chi.URLParam(r, "id")); err != nil {
		return err
	}
	h.record(r, "group.delete", "", chi.URLParam(r, "id"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *HTTP) listGrants(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	gs, err := h.svc.ListGrants(r.Context(), q.Get("subjectType"), q.Get("subjectId"))
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": gs})
	return nil
}

func (h *HTTP) createGrant(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		SubjectType string `json:"subjectType"`
		SubjectID   string `json:"subjectId"`
		Path        string `json:"path"`
		Perm        string `json:"perm"`
	}
	if err := httpx.DecodeJSON(r, &in); err != nil {
		return err
	}
	g, err := h.svc.CreateGrant(r.Context(), httpx.Subject(r).UserID, in.SubjectType, in.SubjectID, in.Path, in.Perm)
	if err != nil {
		return err
	}
	h.record(r, "grant.create", g.Path, g.SubjectType+":"+g.SubjectID+" "+g.PermName)
	httpx.WriteJSON(w, http.StatusCreated, g)
	return nil
}

func (h *HTTP) deleteGrant(w http.ResponseWriter, r *http.Request) error {
	if err := h.svc.DeleteGrant(r.Context(), chi.URLParam(r, "id")); err != nil {
		return err
	}
	h.record(r, "grant.delete", "", chi.URLParam(r, "id"))
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (h *HTTP) listAudit(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	es, err := h.audit.List(r.Context(), audit.Query{UserID: q.Get("userId"), Action: q.Get("action"), Path: q.Get("path"), Before: before, Limit: limit})
	if err != nil {
		return err
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": es})
	return nil
}
