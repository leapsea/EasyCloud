package config

import "testing"

func TestEnvMapping(t *testing.T) {
	t.Setenv("EASYCLOUD_DATA_ROOT", "/srv/data")
	t.Setenv("EASYCLOUD_UPLOAD_MAX_FILE_SIZE", "123")
	t.Setenv("EASYCLOUD_DB_HOST", "db")
	t.Setenv("EASYCLOUD_DB_PASSWORD", "pw")
	t.Setenv("EASYCLOUD_TRASH_RETENTION_DAYS", "7")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.DataRoot != "/srv/data" || c.Upload.MaxFileSize != 123 || c.Trash.RetentionDays != 7 {
		t.Fatalf("%+v", c)
	}
	if c.Database.Driver != "mariadb" || c.Database.Host != "db" || c.Database.Password != "pw" || c.Database.Port != 3306 {
		t.Fatalf("db %+v", c.Database)
	}
	if got := c.Database.Redacted(); got != "mariadb://easycloud@db:3306/easycloud" {
		t.Fatal(got)
	}
}

func TestDefaultsToSQLite(t *testing.T) {
	c, err := Load("")
	if err != nil || c.Database.Driver != "sqlite" || c.Upload.ChunkSize != 16<<20 {
		t.Fatal(c, err)
	}
}
