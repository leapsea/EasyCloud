// Command samba-gateway is an EasyCloud backend plugin that exports granted
// directories over SMB. EasyCloud pushes accounts (NT hashes of app passwords)
// and shares; the plugin renders a Samba configuration and, optionally,
// manages a Samba container through the Docker Engine API.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/easycloud/easycloud/pkg/pluginsdk"
)

type ext struct {
	pluginsdk.Base
	cfg config
	mu  sync.Mutex
}

func writeAtomic(path, content string, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (e *ext) Sync(ctx context.Context, req *pluginsdk.SyncRequest) (*pluginsdk.Empty, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.cfg
	out, err := filepath.Abs(c.OutputDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(out, 0o750); err != nil {
		return nil, err
	}
	uidPath := filepath.Join(out, "uids.json")
	prev, err := loadUIDs(uidPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", uidPath, err)
	}
	uids, changed := assignUIDs(prev, req.Accounts, c.ForceUID, c.ForceGID)
	if changed { // written first: smbpasswd and the scripts must agree with it
		if err := writeAtomic(uidPath, uids.encode(), 0o640); err != nil {
			return nil, fmt.Errorf("write %s: %w", uidPath, err)
		}
	}
	entry, reload := scripts(c, req, uids)
	files := []struct {
		name, content string
		mode          os.FileMode
	}{
		{"smb.conf", smbConf(c, req), 0o644},
		{"smbpasswd", smbpasswd(req, uids), 0o600}, // NT hashes are password-equivalent
		{"entrypoint.sh", entry, 0o755},
		{"reload.sh", reload, 0o755},
	}
	for _, f := range files {
		if err := writeAtomic(filepath.Join(out, f.name), f.content, f.mode); err != nil {
			return nil, fmt.Errorf("write %s: %w", f.name, err)
		}
	}
	if !c.ManageContainer {
		return &pluginsdk.Empty{}, nil
	}
	if c.HostDataRoot == "" {
		c.HostDataRoot = req.DataRoot
	}
	if c.HostConfigDir == "" {
		c.HostConfigDir = out
	}
	if err := newDocker(c.DockerSocket).ensure(ctx, c); err != nil {
		return nil, err
	}
	return &pluginsdk.Empty{}, nil
}

func main() {
	e := &ext{}
	_ = pluginsdk.Config(&e.cfg)
	e.cfg.defaults()
	pluginsdk.Serve(e)
}
