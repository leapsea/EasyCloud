// Package plugin installs, verifies and runs EasyCloud plugins. Three kinds exist:
// viewer (sandboxed ES module in the browser), backend (subprocess over gRPC),
// and connector (configuration for an external service such as OnlyOffice).
package plugin

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"runtime"
	"strings"

	"github.com/easycloud/easycloud/internal/pathguard"
	"golang.org/x/mod/semver"
)

const (
	KindViewer    = "viewer"
	KindBackend   = "backend"
	KindConnector = "connector"
)

type ConfigField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // string | secret | number | bool | url
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

type Manifest struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Type        string            `json:"type"`
	Description string            `json:"description"`
	Author      string            `json:"author"`
	Homepage    string            `json:"homepage,omitempty"`
	Engines     map[string]string `json:"engines"`
	Permissions []string          `json:"permissions"`
	Viewer      *struct {
		Entry      string   `json:"entry"`
		Mimes      []string `json:"mimes"`
		Extensions []string `json:"extensions"`
		Priority   int      `json:"priority"`
		Label      string   `json:"label"`
	} `json:"viewer,omitempty"`
	Backend *struct {
		Executable map[string]string `json:"executable"` // "linux/amd64" -> relative path
		Thumbnail  []string          `json:"thumbnail"`  // MIME patterns
		Events     bool              `json:"events"`
		Accounts   bool              `json:"accounts"` // receives Sync(accounts, shares)
	} `json:"backend,omitempty"`
	Connector *struct {
		Kind string `json:"kind"` // onlyoffice
	} `json:"connector,omitempty"`
	Config []ConfigField `json:"config,omitempty"`
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}$`)

func validRel(p string) bool {
	rp, err := pathguard.Parse(p)
	return err == nil && !rp.IsRoot() && !strings.HasPrefix(p, "/")
}

// Validate checks a manifest against this host.
func (m *Manifest) Validate(hostVersion string) error {
	if !idRe.MatchString(m.ID) {
		return fmt.Errorf("invalid id %q", m.ID)
	}
	if !semver.IsValid("v" + m.Version) {
		return fmt.Errorf("invalid version %q (semver required)", m.Version)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if r := m.Engines["easycloud"]; r != "" {
		ok, err := Satisfies(hostVersion, r)
		if err != nil {
			return fmt.Errorf("engines.easycloud: %v", err)
		}
		if !ok {
			return fmt.Errorf("requires easycloud %s, this is %s", r, hostVersion)
		}
	}
	switch m.Type {
	case KindViewer:
		if m.Viewer == nil || !validRel(m.Viewer.Entry) || path.Ext(m.Viewer.Entry) != ".js" {
			return fmt.Errorf("viewer.entry must be a relative .js file")
		}
		if len(m.Viewer.Mimes) == 0 && len(m.Viewer.Extensions) == 0 {
			return fmt.Errorf("viewer needs mimes or extensions")
		}
	case KindBackend:
		if m.Backend == nil || len(m.Backend.Executable) == 0 {
			return fmt.Errorf("backend.executable is required")
		}
		for plat, p := range m.Backend.Executable {
			if !validRel(p) {
				return fmt.Errorf("backend.executable[%s] is not a safe relative path", plat)
			}
		}
	case KindConnector:
		if m.Connector == nil || m.Connector.Kind == "" {
			return fmt.Errorf("connector.kind is required")
		}
	default:
		return fmt.Errorf("unknown type %q", m.Type)
	}
	keys := map[string]bool{}
	for _, f := range m.Config {
		if f.Key == "" || keys[f.Key] {
			return fmt.Errorf("config keys must be unique and non-empty")
		}
		keys[f.Key] = true
		switch f.Type {
		case "string", "secret", "number", "bool", "url":
		default:
			return fmt.Errorf("config %s: unknown type %q", f.Key, f.Type)
		}
	}
	return nil
}

// Executable returns the binary for this OS/arch.
func (m *Manifest) Executable() (string, bool) {
	if m.Backend == nil {
		return "", false
	}
	p, ok := m.Backend.Executable[runtime.GOOS+"/"+runtime.GOARCH]
	return p, ok
}

func ParseManifest(b []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	return &m, nil
}

// Satisfies evaluates a space-separated comparator list such as ">=0.3.0 <1.0.0".
// Development builds ("dev") satisfy every range.
func Satisfies(version, rng string) (bool, error) {
	if version == "dev" || strings.HasPrefix(version, "dev-") {
		return true, nil
	}
	v := "v" + strings.TrimPrefix(version, "v")
	if !semver.IsValid(v) {
		return false, fmt.Errorf("host version %q is not semver", version)
	}
	for _, c := range strings.Fields(rng) {
		op := strings.TrimRight(c, "0123456789.v-+abcdefghijklmnopqrstuvwxyz")
		want := "v" + strings.TrimPrefix(strings.TrimPrefix(c, op), "v")
		if !semver.IsValid(want) {
			return false, fmt.Errorf("bad comparator %q", c)
		}
		cmp := semver.Compare(v, want)
		var ok bool
		switch op {
		case ">=":
			ok = cmp >= 0
		case ">":
			ok = cmp > 0
		case "<=":
			ok = cmp <= 0
		case "<":
			ok = cmp < 0
		case "=", "":
			ok = cmp == 0
		case "^": // same major (or same minor for 0.x)
			ok = cmp >= 0 && semver.Major(v) == semver.Major(want) && (semver.Major(want) != "v0" || semver.MajorMinor(v) == semver.MajorMinor(want))
		default:
			return false, fmt.Errorf("bad operator in %q", c)
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// MatchMime matches "video/*" style patterns.
func MatchMime(pattern, mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	pattern = strings.ToLower(pattern)
	if strings.HasSuffix(pattern, "/*") {
		return strings.HasPrefix(mime, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == mime
}
