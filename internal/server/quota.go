package server

import (
	"context"
	"net/http"

	"github.com/easycloud/easycloud/internal/identity"
	"github.com/easycloud/easycloud/internal/index"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/platform/errs"
)

// quota limits the bytes stored in each user's personal directory.
type quota struct {
	ids   *identity.Service
	store *index.Store
}

var errQuota = errs.New(http.StatusInsufficientStorage, "QUOTA_EXCEEDED")

func (q quota) Check(ctx context.Context, sub pathguard.Subject, target pathguard.RelPath, add int64) error {
	segs := target.Segments()
	if len(segs) < 2 || segs[0] != identity.HomeRoot {
		return nil
	}
	// The quota belongs to the home's owner, whoever is writing into it.
	users, err := q.ids.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Username != segs[1] || u.QuotaBytes == 0 {
			continue
		}
		used, _, err := q.store.Usage(ctx, u.Home())
		if err != nil {
			return err
		}
		if used+add > u.QuotaBytes {
			e := errQuota.WithDetail(map[string]int64{"quota": u.QuotaBytes, "used": used, "requested": add})
			if sub.IsAdmin {
				e = e.WithAction(errs.Action{Key: "userQuota", Label: "Adjust the user quota", Route: "/admin/users", Focus: u.ID, Role: "admin"})
			}
			return e
		}
	}
	return nil
}

func (q quota) TreeSize(ctx context.Context, p pathguard.RelPath) int64 {
	b, _, _ := q.store.Usage(ctx, p.String())
	return b
}
