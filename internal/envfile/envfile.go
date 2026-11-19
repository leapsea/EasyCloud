// Package envfile reads and edits .env files (KEY=VALUE lines) without losing comments.
package envfile

import (
	"bufio"
	"os"
	"strings"
)

// Read returns the variables defined in path. Quotes around values are removed.
func Read(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := parse(sc.Text())
		if ok {
			out[k] = v
		}
	}
	return out, sc.Err()
}

func parse(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	k, v = strings.TrimSpace(k), strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		v = v[1 : len(v)-1]
	}
	return k, v, k != ""
}

// Apply exports the variables into the process environment (file values win).
func Apply(vars map[string]string) {
	for k, v := range vars {
		_ = os.Setenv(k, v)
	}
}

// Set replaces KEY's active line (or appends it), keeping everything else.
func Set(path, key, value string) error {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(b) == 0 {
		lines = nil
	}
	done := false
	for i, l := range lines {
		if k, _, ok := parse(l); ok && k == key {
			lines[i] = key + "=" + value
			done = true
		}
	}
	if !done {
		lines = append(lines, key+"="+value)
	}
	fi, statErr := os.Stat(path)
	mode := os.FileMode(0o600)
	if statErr == nil {
		mode = fi.Mode().Perm()
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
