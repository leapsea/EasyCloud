package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/easycloud/easycloud/pkg/pluginsdk"
)

var snap = &pluginsdk.SyncRequest{
	DataRoot: "/srv/data",
	Accounts: []pluginsdk.Account{{Username: "alice", NTHash: "8846F7EAEE8FB117AD06BDD830B7586C"}, {Username: "admin", NTHash: "31D6CFE0D16AE931B73C59D7E0C089C0", Admin: true}},
	Shares: []pluginsdk.ShareDef{
		{Name: "data", Path: "", WriteUsers: []string{"admin"}},
		{Name: "alice", Path: "user/alice", WriteUsers: []string{"alice"}},
		{Name: "AA", Path: "share/AA", ReadUsers: []string{"alice"}},
		{Name: "empty", Path: "share/x"},
	},
}

func TestRender(t *testing.T) {
	var c config
	c.defaults()
	conf := smbConf(c, snap)
	for _, want := range []string{"[data]\n   path = /data\n", "[alice]\n   path = /data/user/alice", `valid users = "alice"`, `write list = "alice"`,
		"[AA]\n   path = /data/share/AA", "map to guest = never", "follow symlinks = no", "veto files = /.easycloud/", "force user = easycloud"} {
		if !strings.Contains(conf, want) {
			t.Errorf("smb.conf missing %q", want)
		}
	}
	if strings.Contains(conf, "[empty]") {
		t.Error("share without users rendered")
	}
	aa := conf[strings.Index(conf, "[AA]"):]
	if strings.Contains(strings.SplitN(aa, "\n[", 2)[0], "write list") {
		t.Error("read-only share has a write list")
	}
	uids, _ := assignUIDs(nil, snap.Accounts)
	pw := smbpasswd(snap, uids)
	if !strings.Contains(pw, "alice:") || !strings.Contains(pw, ":8846F7EAEE8FB117AD06BDD830B7586C:[U") {
		t.Fatalf("smbpasswd %q", pw)
	}
	entry, reload := scripts(c, snap, uids)
	if !strings.Contains(entry, "exec smbd --foreground") || !strings.Contains(reload, "reload-config") || !strings.Contains(entry, "adduser -D -H -u") {
		t.Fatal("scripts")
	}
	if !strings.Contains(pw, fmt.Sprintf("alice:%d:", uids["alice"])) || !strings.Contains(entry, fmt.Sprintf("-u %d alice", uids["alice"])) {
		t.Fatal("smbpasswd and scripts disagree on alice's uid")
	}
}

// Two names that collided under the old crc32 scheme used to break the second
// account; allocation keeps every uid unique and stable.
func TestUIDAllocation(t *testing.T) {
	seen := map[int]string{}
	var a, b string
	for i := 0; a == ""; i++ {
		n := fmt.Sprintf("user%d", i)
		if o, ok := seen[legacyUID(n)]; ok {
			a, b = o, n
		}
		seen[legacyUID(n)] = n
	}
	acct := func(names ...string) []pluginsdk.Account {
		var out []pluginsdk.Account
		for _, n := range names {
			out = append(out, pluginsdk.Account{Username: n})
		}
		return out
	}
	unique := func(m uidMap) {
		t.Helper()
		got := map[int]string{}
		for n, u := range m {
			if o, ok := got[u]; ok {
				t.Fatalf("%s and %s share uid %d", o, n, u)
			}
			if u < uidBase {
				t.Fatalf("%s got uid %d", n, u)
			}
			got[u] = n
		}
	}

	// First run (no uids.json): legacy uids are kept where unique, so users
	// already created in a running container stay consistent.
	m, changed := assignUIDs(nil, acct(b, a, "carol"))
	if !changed {
		t.Fatal("first run must write uids.json")
	}
	unique(m)
	first, second := a, b
	if second < first {
		first, second = second, first
	}
	if m[first] != legacyUID(first) || m["carol"] != legacyUID("carol") {
		t.Fatalf("legacy uids not kept: %v", m)
	}
	if m[second] == legacyUID(second) {
		t.Fatal("colliding name kept the duplicate uid")
	}

	// Stable across syncs.
	again, changed := assignUIDs(m, acct("carol", a, b))
	if changed || again[a] != m[a] || again[b] != m[b] {
		t.Fatalf("uids changed on resync: %v", again)
	}

	// A removed account keeps its entry; its uid is never handed out again.
	carol := m["carol"]
	m, _ = assignUIDs(m, acct(a, b, "dave"))
	unique(m)
	if m["carol"] != carol || m["dave"] == carol {
		t.Fatalf("carol's uid reused: %v", m)
	}

	// Reserved uids (force user/group) are skipped.
	m, _ = assignUIDs(uidMap{"x": uidBase}, acct("x", "y"), uidBase+1)
	if m["y"] != uidBase+2 {
		t.Fatalf("y = %d, want %d", m["y"], uidBase+2)
	}
}

func TestSyncWritesConsistentUIDs(t *testing.T) {
	dir := t.TempDir()
	e := &ext{}
	e.cfg.OutputDir = dir
	e.cfg.defaults()
	req := &pluginsdk.SyncRequest{Accounts: []pluginsdk.Account{{Username: "alice", NTHash: "AA"}, {Username: "bob", NTHash: "BB"}}}
	for i := 0; i < 2; i++ {
		if _, err := e.Sync(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	m, err := loadUIDs(filepath.Join(dir, "uids.json"))
	if err != nil || len(m) != 2 {
		t.Fatalf("uids.json: %v %v", m, err)
	}
	pw, _ := os.ReadFile(filepath.Join(dir, "smbpasswd"))
	entry, _ := os.ReadFile(filepath.Join(dir, "entrypoint.sh"))
	for n, u := range m {
		if !strings.Contains(string(pw), fmt.Sprintf("%s:%d:", n, u)) || !strings.Contains(string(entry), fmt.Sprintf("-u %d %s", u, n)) {
			t.Fatalf("%s: uid %d not used consistently", n, u)
		}
	}
}

func TestSyncManagesContainer(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(os.TempDir(), "ecdock.sock")
	os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("unix socket unavailable:", err)
	}
	defer os.Remove(sock)
	var mu sync.Mutex
	var calls []string
	created := false
	var spec map[string]any
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/json"):
			if !created {
				w.WriteHeader(404)
				return
			}
			w.Write([]byte(`{"State":{"Running":true}}`))
		case strings.HasSuffix(r.URL.Path, "/containers/create"):
			json.NewDecoder(r.Body).Decode(&spec)
			created = true
			w.WriteHeader(201)
			w.Write([]byte(`{"Id":"abc"}`))
		case strings.HasSuffix(r.URL.Path, "/exec"):
			w.WriteHeader(201)
			w.Write([]byte(`{"Id":"ex1"}`))
		default:
			w.WriteHeader(204)
		}
	}))
	e := &ext{cfg: config{OutputDir: dir, ManageContainer: true, DockerSocket: sock, HostDataRoot: "/mnt/ssd/nasData"}}
	e.cfg.defaults()
	ctx := context.Background()
	if _, err := e.Sync(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Sync(ctx, snap); err != nil { // second sync reloads the running container
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	got := strings.Join(calls, "\n")
	for _, want := range []string{"POST /v1.43/containers/create", "POST /v1.43/containers/easycloud-samba/start", "POST /v1.43/containers/easycloud-samba/exec", "POST /v1.43/exec/ex1/start"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing docker call %q in\n%s", want, got)
		}
	}
	binds := spec["HostConfig"].(map[string]any)["Binds"].([]any)
	if binds[1] != "/mnt/ssd/nasData:/data" || !strings.HasSuffix(binds[0].(string), ":/config:ro") {
		t.Fatalf("binds %v", binds)
	}
	if fi, err := os.Stat(filepath.Join(dir, "smbpasswd")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("smbpasswd perms: %v %v", fi, err)
	}
}
