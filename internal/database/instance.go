package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type instanceFile struct {
	ID string `json:"id"`
}

func instancePath(dataRoot string) string {
	return filepath.Join(dataRoot, ".easycloud", "instance.json")
}

// BindInstance makes sure the database and the data root belong together, so a
// database pointed at another NAS's data directory is refused instead of corrupting it.
func (d *DB) BindInstance(ctx context.Context, dataRoot string, force bool) error {
	var dbID string
	err := d.R.QueryRowContext(ctx, "SELECT id FROM easycloud_instance LIMIT 1").Scan(&dbID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var f instanceFile
	b, ferr := os.ReadFile(instancePath(dataRoot))
	if ferr == nil {
		if json.Unmarshal(b, &f) != nil || f.ID == "" {
			return fmt.Errorf("%w: %s is corrupt", ErrConfig, instancePath(dataRoot))
		}
	} else if !errors.Is(ferr, os.ErrNotExist) {
		return ferr
	}

	switch {
	case dbID != "" && f.ID == dbID:
		return nil
	case dbID == "" && f.ID == "":
		id := newInstanceID()
		if err := writeInstance(dataRoot, id); err != nil {
			return err
		}
		return d.setInstance(ctx, id)
	case dbID == "":
		return d.setInstance(ctx, f.ID) // fresh database adopting existing data
	case force:
		id := f.ID
		if id == "" {
			id = dbID
			if err := writeInstance(dataRoot, id); err != nil {
				return err
			}
		}
		return d.setInstance(ctx, id)
	}
	return fmt.Errorf("%w: database instance %s does not match data root (%q); if intended, run `easycloud rebind`",
		ErrConfig, dbID, f.ID)
}

func (d *DB) setInstance(ctx context.Context, id string) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM easycloud_instance"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO easycloud_instance (id, created_at) VALUES (?, ?)", id, time.Now().Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func writeInstance(dataRoot, id string) error {
	p := instancePath(dataRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(instanceFile{ID: id})
	return os.WriteFile(p, b, 0o600)
}

func newInstanceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
