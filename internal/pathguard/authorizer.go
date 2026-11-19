package pathguard

import (
	"context"
	"os"
	"sort"
)

type Perm uint8

const (
	PermRead Perm = 1 << iota
	PermWrite
	PermRW = PermRead | PermWrite
)

func (p Perm) Has(need Perm) bool { return p&need == need }

func (p Perm) String() string {
	switch {
	case p.Has(PermRW):
		return "rw"
	case p.Has(PermRead):
		return "r"
	}
	return ""
}

// Subject identifies the caller.
type Subject struct {
	UserID   string
	Username string
	IsAdmin  bool
}

// Handle is the only object that touches user files. Root is opened on the
// granted directory, so ".." and symlinks cannot escape it even if an
// authorization check upstream is wrong. Callers must Close it.
type Handle struct {
	Root  *os.Root
	Rel   string  // path relative to Root ("." for the root itself)
	Perm  Perm    // effective permission on the requested path
	Base  RelPath // the granted directory Root is opened on
	close func()
}

func (h Handle) Close() {
	if h.close != nil {
		h.close()
	}
}

// Visibility describes what a subject may see in a directory listing.
type Visibility struct {
	// Perm > 0: the directory lies inside a grant and can be listed in full.
	Perm Perm
	// Perm == 0: navigation only — list just these child names, each leading to a grant.
	Children map[string]bool
}

// Authorizer: V1 injected AdminAuthorizer; V2 injects GrantAuthorizer.
type Authorizer interface {
	// Check returns ErrNotFound (never "forbidden") when access is denied.
	Check(ctx context.Context, sub Subject, p RelPath, need Perm) (Handle, error)
	// Visible returns ErrNotFound when dir is neither inside nor above any grant.
	Visible(ctx context.Context, sub Subject, dir RelPath) (Visibility, error)
	// Roots returns the top-most readable paths (used to scope search).
	Roots(ctx context.Context, sub Subject) ([]RelPath, error)
}

// AdminAuthorizer grants full rw on the data root to administrators only.
type AdminAuthorizer struct{ root *os.Root }

func NewAdminAuthorizer(root *os.Root) *AdminAuthorizer { return &AdminAuthorizer{root: root} }

func (a *AdminAuthorizer) Check(_ context.Context, sub Subject, p RelPath, _ Perm) (Handle, error) {
	if !sub.IsAdmin {
		return Handle{}, ErrNotFound
	}
	return Handle{Root: a.root, Rel: p.String(), Perm: PermRW}, nil
}

func (a *AdminAuthorizer) Roots(_ context.Context, sub Subject) ([]RelPath, error) {
	if !sub.IsAdmin {
		return nil, nil
	}
	return []RelPath{{}}, nil
}

func (a *AdminAuthorizer) Visible(_ context.Context, sub Subject, _ RelPath) (Visibility, error) {
	if !sub.IsAdmin {
		return Visibility{}, ErrNotFound
	}
	return Visibility{Perm: PermRW}, nil
}

// Grant is one authorization entry, already resolved for a user (including department grants).
type Grant struct {
	Path RelPath
	Perm Perm
}

// GrantSource returns every grant that applies to a user, directly or via departments.
type GrantSource interface {
	GrantsFor(ctx context.Context, userID string) ([]Grant, error)
}

// GrantAuthorizer: administrators see the whole data root; everyone else sees
// the union of their grants. Matching is by path segment.
type GrantAuthorizer struct {
	root   *os.Root
	grants GrantSource
}

func NewGrantAuthorizer(root *os.Root, g GrantSource) *GrantAuthorizer {
	return &GrantAuthorizer{root: root, grants: g}
}

func (a *GrantAuthorizer) Check(ctx context.Context, sub Subject, p RelPath, need Perm) (Handle, error) {
	if sub.IsAdmin {
		return Handle{Root: a.root, Rel: p.String(), Perm: PermRW}, nil
	}
	if sub.UserID == "" {
		return Handle{}, ErrNotFound
	}
	gs, err := a.grants.GrantsFor(ctx, sub.UserID)
	if err != nil {
		return Handle{}, err
	}
	var eff Perm
	var base *RelPath
	for i := range gs {
		g := &gs[i]
		if !p.HasPrefix(g.Path) {
			continue
		}
		eff |= g.Perm
		// Open the root on the shallowest grant that alone satisfies the need,
		// so moves inside it stay inside a single os.Root.
		if g.Perm.Has(need) && (base == nil || len(g.Path.segs) < len(base.segs)) {
			base = &g.Path
		}
	}
	if base == nil || !eff.Has(need) {
		return Handle{}, ErrNotFound
	}
	r, err := a.root.OpenRoot(base.String())
	if err != nil {
		return Handle{}, ErrNotFound // granted directory missing or replaced by an escaping symlink
	}
	rel := RelPath{segs: p.segs[len(base.segs):]}
	return Handle{Root: r, Rel: rel.String(), Perm: eff, Base: *base, close: func() { r.Close() }}, nil
}

func (a *GrantAuthorizer) Visible(ctx context.Context, sub Subject, dir RelPath) (Visibility, error) {
	if sub.IsAdmin {
		return Visibility{Perm: PermRW}, nil
	}
	if sub.UserID == "" {
		return Visibility{}, ErrNotFound
	}
	gs, err := a.grants.GrantsFor(ctx, sub.UserID)
	if err != nil {
		return Visibility{}, err
	}
	var eff Perm
	children := map[string]bool{}
	for _, g := range gs {
		switch {
		case dir.HasPrefix(g.Path):
			eff |= g.Perm
		case g.Path.HasPrefix(dir):
			// dir is a strict ancestor of the grant: expose only the next segment.
			children[g.Path.segs[len(dir.segs)]] = true
		}
	}
	if eff.Has(PermRead) {
		return Visibility{Perm: eff}, nil
	}
	if len(children) == 0 {
		return Visibility{}, ErrNotFound
	}
	return Visibility{Children: children}, nil
}

func (a *GrantAuthorizer) Roots(ctx context.Context, sub Subject) ([]RelPath, error) {
	if sub.IsAdmin {
		return []RelPath{{}}, nil
	}
	if sub.UserID == "" {
		return nil, nil
	}
	gs, err := a.grants.GrantsFor(ctx, sub.UserID)
	if err != nil {
		return nil, err
	}
	var out []RelPath
	for _, g := range gs {
		if !g.Perm.Has(PermRead) {
			continue
		}
		covered := false
		for _, o := range gs {
			if o.Perm.Has(PermRead) && len(o.Path.segs) < len(g.Path.segs) && g.Path.HasPrefix(o.Path) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, g.Path)
		}
	}
	return out, nil
}

// SortedChildren is a helper for deterministic listings.
func (v Visibility) SortedChildren() []string {
	out := make([]string, 0, len(v.Children))
	for c := range v.Children {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}
