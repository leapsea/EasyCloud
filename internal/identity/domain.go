// Package identity owns users, departments (groups), sessions and directory grants.
package identity

import (
	"errors"
	"regexp"
	"time"

	"github.com/easycloud/easycloud/internal/pathguard"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	SubjectUser  = "user"
	SubjectGroup = "group"

	// HomeRoot is where personal spaces live: user/<username>.
	HomeRoot = "user"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrDuplicate      = errors.New("duplicate")
	ErrLastAdmin      = errors.New("last administrator")
	ErrInvalid        = errors.New("invalid input")
	ErrBadCredentials = errors.New("bad credentials")
	ErrRateLimited    = errors.New("too many attempts")
)

// Usernames double as path segments (user/<name>), so they are deliberately strict.
var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

func ValidUsername(s string) bool { return usernameRe.MatchString(s) && s != "." && s != ".." }

type User struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	Role        string    `json:"role"`
	Disabled    bool      `json:"disabled"`
	QuotaBytes  int64     `json:"quotaBytes"`
	CreatedAt   time.Time `json:"createdAt"`
	GroupIDs    []string  `json:"groupIds"`
	TwoFactor   bool      `json:"twoFactor"` // filled in for the admin user list
	hash        string
}

func (u User) Subject() pathguard.Subject {
	return pathguard.Subject{UserID: u.ID, Username: u.Username, IsAdmin: u.Role == RoleAdmin}
}

func (u User) Home() string { return HomeRoot + "/" + u.Username }

type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ParentID  string    `json:"parentId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	MemberIDs []string  `json:"memberIds"`
}

type Grant struct {
	ID          string         `json:"id"`
	SubjectType string         `json:"subjectType"`
	SubjectID   string         `json:"subjectId"`
	Path        string         `json:"path"` // display form, leading slash
	Perm        pathguard.Perm `json:"-"`
	PermName    string         `json:"perm"` // "r" | "rw"
	CreatedBy   string         `json:"createdBy"`
	CreatedAt   time.Time      `json:"createdAt"`
}
