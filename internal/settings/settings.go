// Package settings resolves runtime settings: Dashboard override > env > yaml > default.
package settings

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/easycloud/easycloud/internal/config"
	"github.com/easycloud/easycloud/internal/database"
)

type Key[T any] struct {
	Name     string
	Fallback func(c *config.Config) T
}

var (
	UploadMaxFileSize  = Key[int64]{"upload.max_file_size", func(c *config.Config) int64 { return c.Upload.MaxFileSize }}
	UploadChunkSize    = Key[int64]{"upload.chunk_size", func(c *config.Config) int64 { return c.Upload.ChunkSize }}
	TrashRetentionDays = Key[int64]{"trash.retention_days", func(c *config.Config) int64 { return int64(c.Trash.RetentionDays) }}
	MetricsRawDays     = Key[int64]{"metrics.raw_retention_days", func(c *config.Config) int64 { return int64(c.Metrics.RawRetentionDays) }}
	MetricsMinuteDays  = Key[int64]{"metrics.minute_retention_days", func(c *config.Config) int64 { return int64(c.Metrics.MinuteRetentionDays) }}

	// All lists the Dashboard-overridable keys in display order.
	All = []Key[int64]{UploadMaxFileSize, UploadChunkSize, TrashRetentionDays, MetricsRawDays, MetricsMinuteDays}
)

type Store interface {
	Get(ctx context.Context, name string) (raw string, ok bool, err error)
	Set(ctx context.Context, name, raw, by string) error
	Reset(ctx context.Context, name string) error
}

// SQLStore caches overrides in memory; writes go through it so the cache stays valid.
type SQLStore struct {
	db    *database.DB
	mu    sync.RWMutex
	cache map[string]string
}

func NewSQLStore(db *database.DB) *SQLStore { return &SQLStore{db: db} }

func (s *SQLStore) load(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache != nil {
		return nil
	}
	rows, err := s.db.R.QueryContext(ctx, "SELECT name, value FROM easycloud_settings")
	if err != nil {
		return err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		m[k] = v
	}
	s.cache = m
	return rows.Err()
}

func (s *SQLStore) Get(ctx context.Context, name string) (string, bool, error) {
	if err := s.load(ctx); err != nil {
		return "", false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.cache[name]
	return v, ok, nil
}

func (s *SQLStore) Set(ctx context.Context, name, raw, by string) error {
	if err := s.load(ctx); err != nil {
		return err
	}
	q := s.db.Upsert("easycloud_settings", []string{"name", "value", "updated_by", "updated_at"}, []string{"name"})
	_, err := s.db.W.ExecContext(ctx, q, name, raw, by, time.Now().Unix())
	if err == nil {
		s.mu.Lock()
		s.cache[name] = raw
		s.mu.Unlock()
	}
	return err
}

func (s *SQLStore) Reset(ctx context.Context, name string) error {
	if err := s.load(ctx); err != nil {
		return err
	}
	_, err := s.db.W.ExecContext(ctx, "DELETE FROM easycloud_settings WHERE name = ?", name)
	if err == nil {
		s.mu.Lock()
		delete(s.cache, name)
		s.mu.Unlock()
	}
	return err
}

// Resolver binds a Store to the startup config.
type Resolver struct {
	Store Store
	Cfg   *config.Config
}

// Int returns the effective value and whether it comes from a Dashboard override.
func (r Resolver) Int(ctx context.Context, k Key[int64]) (int64, bool, error) {
	raw, ok, err := r.Store.Get(ctx, k.Name)
	if err != nil || !ok {
		return k.Fallback(r.Cfg), false, err
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return k.Fallback(r.Cfg), false, errors.New("settings: bad stored value for " + k.Name)
	}
	return v, true, nil
}
