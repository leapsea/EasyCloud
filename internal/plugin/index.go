package plugin

import (
	"fmt"
	"sort"
)

// RepoEntry describes one published package version. The release pipeline
// uploads one entry next to each package; the repository index is rebuilt
// from all entries, so the published packages stay the single source of truth.
type RepoEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Version     string `json:"version"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	Engine      string `json:"engine"`
}

// Index is the repository document the host fetches.
type Index struct {
	Plugins []RepoPlugin `json:"plugins"`
}

// EntryFor describes the package data published at url.
func EntryFor(data []byte, url string) (RepoEntry, error) {
	pkg, err := Open(data)
	if err != nil {
		return RepoEntry{}, err
	}
	m := pkg.Manifest
	return RepoEntry{
		ID: m.ID, Name: m.Name, Type: m.Type, Description: m.Description,
		Version: m.Version, URL: url, SHA256: sha256Hex(data), Engine: m.Engines["easycloud"],
	}, nil
}

// BuildIndex groups entries per plugin, newest version first. Name, type and
// description come from the newest version. Publishing the same version twice
// with different content is an error: installed copies could not be told apart.
func BuildIndex(entries []RepoEntry) (Index, error) {
	byID := map[string][]RepoEntry{}
	for _, e := range entries {
		if e.ID == "" || e.Version == "" || e.URL == "" || e.SHA256 == "" {
			return Index{}, fmt.Errorf("incomplete entry %+v", e)
		}
		byID[e.ID] = append(byID[e.ID], e)
	}
	idx := Index{Plugins: []RepoPlugin{}} // "plugins": [] rather than null when empty
	for id, es := range byID {
		sort.SliceStable(es, func(i, j int) bool { return compareSemver(es[i].Version, es[j].Version) > 0 })
		rp := RepoPlugin{ID: id, Name: es[0].Name, Type: es[0].Type, Description: es[0].Description}
		for i, e := range es {
			if i > 0 && e.Version == es[i-1].Version {
				if e.SHA256 != es[i-1].SHA256 {
					return Index{}, fmt.Errorf("%s %s published twice with different content", id, e.Version)
				}
				continue
			}
			rp.Versions = append(rp.Versions, RepoVersion{Version: e.Version, URL: e.URL, SHA256: e.SHA256, Engine: e.Engine})
		}
		idx.Plugins = append(idx.Plugins, rp)
	}
	sort.Slice(idx.Plugins, func(i, j int) bool { return idx.Plugins[i].ID < idx.Plugins[j].ID })
	return idx, nil
}
