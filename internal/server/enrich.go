package server

import (
	"context"

	"github.com/easycloud/easycloud/internal/index"
	"github.com/easycloud/easycloud/internal/pathguard"
	"github.com/easycloud/easycloud/internal/storage/domain"
	"github.com/easycloud/easycloud/internal/thumb"
)

type enricher struct {
	store  *index.Store
	thumbs *thumb.Service
}

func (e enricher) Enrich(ctx context.Context, dir pathguard.RelPath, entries []domain.FileEntry) {
	nodes, err := e.store.Children(ctx, dir.String())
	dims := map[string][2]int{}
	if err == nil {
		for _, n := range nodes {
			if n.Width > 0 {
				dims[n.Name] = [2]int{n.Width, n.Height}
			}
		}
	}
	for i := range entries {
		if entries[i].IsDir {
			continue
		}
		if d, ok := dims[entries[i].Name]; ok {
			entries[i].Width, entries[i].Height = d[0], d[1]
		}
		entries[i].Thumb = e.thumbs.Supported(entries[i].Name)
	}
}
