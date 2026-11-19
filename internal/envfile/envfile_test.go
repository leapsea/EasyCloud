package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSet(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("# comment\nEASYCLOUD_DB_HOST=db\nexport A=\"quoted value\"\n# EASYCLOUD_DB_DRIVER=mariadb\n"), 0o640)
	v, err := Read(p)
	if err != nil || v["EASYCLOUD_DB_HOST"] != "db" || v["A"] != "quoted value" || len(v) != 2 {
		t.Fatal(v, err)
	}
	if err := Set(p, "EASYCLOUD_DB_DRIVER", "sqlite"); err != nil {
		t.Fatal(err)
	}
	Set(p, "EASYCLOUD_DB_HOST", "db2")
	b, _ := os.ReadFile(p)
	want := "# comment\nEASYCLOUD_DB_HOST=db2\nexport A=\"quoted value\"\n# EASYCLOUD_DB_DRIVER=mariadb\nEASYCLOUD_DB_DRIVER=sqlite\n"
	if string(b) != want {
		t.Fatalf("got\n%s", b)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o640 {
		t.Fatal("mode not preserved")
	}
}
