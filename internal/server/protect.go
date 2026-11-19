package server

import (
	"context"

	"github.com/easycloud/easycloud/internal/identity"
	"github.com/easycloud/easycloud/internal/pathguard"
)

// protector marks /user and every existing user's personal space as system
// directories: deleting, moving or renaming them would break the accounts.
type protector struct{ ids *identity.Service }

func (p protector) Protected(ctx context.Context, path pathguard.RelPath) bool {
	segs := path.Segments()
	if len(segs) == 0 || segs[0] != identity.HomeRoot || len(segs) > 2 {
		return false
	}
	if len(segs) == 1 {
		return true // /user itself
	}
	users, err := p.ids.ListUsers(ctx)
	if err != nil {
		return true // fail closed
	}
	for _, u := range users {
		if u.Username == segs[1] {
			return true
		}
	}
	return false
}
