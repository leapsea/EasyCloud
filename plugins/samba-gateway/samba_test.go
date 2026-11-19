package main

import (
	"context"
	"encoding/json"
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
	pw := smbpasswd(snap)
	if !strings.Contains(pw, "alice:") || !strings.Contains(pw, ":8846F7EAEE8FB117AD06BDD830B7586C:[U") {
		t.Fatalf("smbpasswd %q", pw)
	}
	entry, reload := scripts(c, snap)
	if !strings.Contains(entry, "exec smbd --foreground") || !strings.Contains(reload, "reload-config") || !strings.Contains(entry, "adduser -D -H -u") {
		t.Fatal("scripts")
	}
	if uidFor("alice") != uidFor("alice") || uidFor("alice") < 20000 {
		t.Fatal("uid not stable")
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
