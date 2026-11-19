// Package config merges: built-in defaults < yaml file < EASYCLOUD_* environment.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Database: driver "" means auto — MariaDB when a host is configured, otherwise SQLite.
type Database struct {
	Driver         string `koanf:"driver"`
	Host           string `koanf:"host"`
	Port           int    `koanf:"port"`
	User           string `koanf:"user"`
	Password       string `koanf:"password"`
	Name           string `koanf:"name"`
	ConnectTimeout int    `koanf:"connect_timeout"` // seconds
}

// Redacted returns a log-safe target description.
func (d Database) Redacted() string {
	if d.Driver == "sqlite" {
		return "sqlite"
	}
	return fmt.Sprintf("mariadb://%s@%s:%d/%s", d.User, d.Host, d.Port, d.Name)
}

type Config struct {
	DataRoot string `koanf:"data_root"`
	Paths    struct {
		AppData string `koanf:"app_data"`
	} `koanf:"paths"`
	HTTP struct {
		Listen string `koanf:"listen"`
	} `koanf:"http"`
	Database Database `koanf:"database"`
	Upload   struct {
		MaxFileSize int64 `koanf:"max_file_size"`
		ChunkSize   int64 `koanf:"chunk_size"`
	} `koanf:"upload"`
	Trash struct {
		RetentionDays int `koanf:"retention_days"`
	} `koanf:"trash"`
	Plugins struct {
		AllowUnsigned bool   `koanf:"allow_unsigned"`
		TrustedKeys   string `koanf:"trusted_keys"` // comma-separated base64 ed25519 public keys
		Repository    string `koanf:"repository"`   // index.json URL
	} `koanf:"plugins"`
	Metrics struct {
		RawRetentionDays    int `koanf:"raw_retention_days"`
		MinuteRetentionDays int `koanf:"minute_retention_days"`
	} `koanf:"metrics"`
	Admin struct {
		Password string `koanf:"password"`
	} `koanf:"admin"`
	Log struct {
		Level string `koanf:"level"`
		// Dir additionally writes rotated log files there, but only when the
		// directory already exists (e.g. a mounted /logs volume).
		Dir string `koanf:"dir"`
	} `koanf:"log"`
}

// Load reads the optional yaml at yamlPath (EASYCLOUD_CONFIG overrides it).
func Load(yamlPath string) (*Config, error) {
	k := koanf.New(".")
	_ = k.Load(confmap.Provider(map[string]any{
		"data_root":                     "/data",
		"paths.app_data":                "/var/lib/easycloud",
		"http.listen":                   ":8080",
		"database.driver":               "",
		"database.port":                 3306,
		"database.name":                 "easycloud",
		"database.user":                 "easycloud",
		"database.connect_timeout":      60,
		"upload.max_file_size":          0,
		"upload.chunk_size":             16 << 20,
		"trash.retention_days":          30,
		"plugins.repository":            "https://raw.githubusercontent.com/leapsea/EasyCloud/plugin-index/index.json",
		"metrics.raw_retention_days":    7,
		"metrics.minute_retention_days": 90,
		"log.level":                     "info",
	}, "."), nil)

	if p := os.Getenv("EASYCLOUD_CONFIG"); p != "" {
		yamlPath = p
	}
	if yamlPath != "" {
		if _, err := os.Stat(yamlPath); err == nil {
			if err := k.Load(file.Provider(yamlPath), yaml.Parser()); err != nil {
				return nil, fmt.Errorf("read %s: %w", yamlPath, err)
			}
		}
	}
	// EASYCLOUD_DATA_ROOT -> data_root; EASYCLOUD_HTTP_LISTEN -> http.listen etc.
	// Section prefixes are mapped explicitly so underscores inside keys survive.
	err := k.Load(env.ProviderWithValue("EASYCLOUD_", ".", func(key, v string) (string, any) {
		key = strings.ToLower(strings.TrimPrefix(key, "EASYCLOUD_"))
		for _, sec := range []string{"paths", "http", "upload", "trash", "admin", "log", "metrics", "plugins"} {
			if strings.HasPrefix(key, sec+"_") {
				return sec + "." + strings.TrimPrefix(key, sec+"_"), v
			}
		}
		if strings.HasPrefix(key, "db_") {
			return "database." + strings.TrimPrefix(key, "db_"), v
		}
		switch key {
		case "app_data":
			return "paths.app_data", v
		case "config":
			return "", nil
		}
		return key, v
	}), nil)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := k.Unmarshal("", &c); err != nil {
		return nil, err
	}
	switch c.Database.Driver {
	case "":
		if c.Database.Host != "" {
			c.Database.Driver = "mariadb"
		} else {
			c.Database.Driver = "sqlite"
		}
	case "mysql":
		c.Database.Driver = "mariadb"
	case "sqlite", "mariadb":
	default:
		return nil, fmt.Errorf("database.driver %q is not supported (sqlite or mariadb)", c.Database.Driver)
	}
	if c.Database.Driver == "mariadb" && c.Database.Host == "" {
		return nil, fmt.Errorf("database.driver is mariadb but EASYCLOUD_DB_HOST is not set")
	}
	return &c, nil
}
