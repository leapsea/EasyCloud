package identity

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/easycloud/easycloud/pkg/pluginsdk"
)

var shareNameBad = regexp.MustCompile(`[^\p{L}\p{N}_.-]+`)

// ProtocolSnapshot builds the desired SMB state: one account per user that has
// an app password (newest wins), a home share per user, one share per granted
// directory (department grants expanded to members of sub-departments too), and
// a "data" share of the whole root for administrators.
func (s *Service) ProtocolSnapshot(ctx context.Context) (*pluginsdk.SyncRequest, error) {
	users, err := s.r.listUsers(ctx)
	if err != nil {
		return nil, err
	}
	hashes := map[string]string{}
	rows, err := s.db.R.QueryContext(ctx, "SELECT user_id, nt_hash FROM easycloud_app_passwords ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var uid, h string
		if err := rows.Scan(&uid, &h); err == nil {
			hashes[uid] = h // later rows (newer) overwrite
		}
	}
	rows.Close()

	byID := map[string]User{}
	req := &pluginsdk.SyncRequest{Accounts: []pluginsdk.Account{}, Shares: []pluginsdk.ShareDef{}}
	var admins []string
	for _, u := range users {
		h, ok := hashes[u.ID]
		if u.Disabled || !ok {
			continue
		}
		byID[u.ID] = u
		req.Accounts = append(req.Accounts, pluginsdk.Account{Username: u.Username, NTHash: h, Admin: u.Role == RoleAdmin})
		if u.Role == RoleAdmin {
			admins = append(admins, u.Username)
		}
	}

	groups, err := s.r.listGroups(ctx)
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	members := map[string][]string{}
	for _, g := range groups {
		children[g.ParentID] = append(children[g.ParentID], g.ID)
		members[g.ID] = g.MemberIDs
	}
	// expand returns the members of g and of every descendant department.
	var expand func(g string, seen map[string]bool) []string
	expand = func(g string, seen map[string]bool) []string {
		if seen[g] {
			return nil
		}
		seen[g] = true
		out := append([]string{}, members[g]...)
		for _, c := range children[g] {
			out = append(out, expand(c, seen)...)
		}
		return out
	}

	type acc struct{ read, write map[string]bool }
	byPath := map[string]*acc{}
	grants, err := s.r.listGrants(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, g := range grants {
		var uids []string
		if g.SubjectType == SubjectUser {
			uids = []string{g.SubjectID}
		} else {
			uids = expand(g.SubjectID, map[string]bool{})
		}
		a := byPath[g.Path]
		if a == nil {
			a = &acc{read: map[string]bool{}, write: map[string]bool{}}
			byPath[g.Path] = a
		}
		for _, uid := range uids {
			u, ok := byID[uid]
			if !ok {
				continue
			}
			if g.PermName == "rw" {
				a.write[u.Username] = true
			} else {
				a.read[u.Username] = true
			}
		}
	}

	used := map[string]bool{}
	name := func(path string) string {
		segs := strings.Split(strings.Trim(path, "/"), "/")
		n := segs[len(segs)-1]
		if len(segs) == 2 && segs[0] == HomeRoot {
			n = segs[1] // personal space: share named after the user
		} else if used[strings.ToLower(n)] {
			n = strings.Join(segs, "-")
		}
		n = shareNameBad.ReplaceAllString(n, "_")
		base := n
		for i := 2; used[strings.ToLower(n)]; i++ {
			n = base + "-" + itoa(i)
		}
		used[strings.ToLower(n)] = true
		return n
	}
	used["data"], used["homes"], used["global"], used["ipc$"] = true, true, true, true
	if len(admins) > 0 {
		sort.Strings(admins)
		req.Shares = append(req.Shares, pluginsdk.ShareDef{Name: "data", Path: "", WriteUsers: admins, ReadUsers: []string{}})
	}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		a := byPath[p]
		if len(a.read) == 0 && len(a.write) == 0 {
			continue
		}
		req.Shares = append(req.Shares, pluginsdk.ShareDef{Name: name(p), Path: strings.TrimPrefix(p, "/"), ReadUsers: keys(a.read, a.write), WriteUsers: keys(a.write, nil)})
	}
	return req, nil
}

func keys(m, exclude map[string]bool) []string {
	out := []string{}
	for k := range m {
		if !exclude[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func itoa(i int) string {
	const d = "0123456789"
	if i < 10 {
		return d[i : i+1]
	}
	return itoa(i/10) + d[i%10:i%10+1]
}
