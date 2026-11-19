package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// docker is a minimal Docker Engine API client over the unix socket.
type docker struct{ c *http.Client }

func newDocker(socket string) docker {
	return docker{c: &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socket)
		},
	}}}
}

func (d docker) call(ctx context.Context, method, path string, body any, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.43"+path, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := d.c.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if out != nil && res.StatusCode < 300 && len(b) > 0 {
		_ = json.Unmarshal(b, out)
	}
	if res.StatusCode >= 400 {
		return res.StatusCode, fmt.Errorf("docker %s %s: %d %s", method, path, res.StatusCode, bytes.TrimSpace(b))
	}
	return res.StatusCode, nil
}

// ensure creates (if needed) and starts the container, or reloads a running one.
func (d docker) ensure(ctx context.Context, c config) error {
	var info struct {
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
	}
	name := url.PathEscape(c.ContainerName)
	code, err := d.call(ctx, "GET", "/containers/"+name+"/json", nil, &info)
	if code == 404 {
		port := strconv.Itoa(c.SMBPort)
		spec := map[string]any{
			"Image":        c.Image,
			"Entrypoint":   []string{"/bin/sh", "/config/entrypoint.sh"},
			"ExposedPorts": map[string]any{"445/tcp": map[string]any{}},
			"Labels":       map[string]string{"io.easycloud.managed": "samba-gateway"},
			"HostConfig": map[string]any{
				"Binds":         []string{c.HostConfigDir + ":/config:ro", c.HostDataRoot + ":" + c.ContainerData},
				"PortBindings":  map[string]any{"445/tcp": []map[string]string{{"HostPort": port}}},
				"RestartPolicy": map[string]string{"Name": "unless-stopped"},
			},
		}
		if _, err := d.call(ctx, "POST", "/containers/create?name="+name, spec, nil); err != nil {
			return fmt.Errorf("create container (is the image %s available?): %w", c.Image, err)
		}
		_, err = d.call(ctx, "POST", "/containers/"+name+"/start", nil, nil)
		return err
	}
	if err != nil {
		return err
	}
	if !info.State.Running {
		_, err = d.call(ctx, "POST", "/containers/"+name+"/start", nil, nil)
		return err
	}
	var ex struct {
		ID string `json:"Id"`
	}
	if _, err := d.call(ctx, "POST", "/containers/"+name+"/exec", map[string]any{"Cmd": []string{"/bin/sh", "/config/reload.sh"}}, &ex); err != nil {
		return err
	}
	_, err = d.call(ctx, "POST", "/exec/"+ex.ID+"/start", map[string]any{"Detach": true}, nil)
	return err
}
