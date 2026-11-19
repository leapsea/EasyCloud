package plugin

import "testing"

func TestBuildIndex(t *testing.T) {
	e := func(id, v, sum string) RepoEntry {
		return RepoEntry{ID: id, Name: id + " " + v, Version: v, URL: "https://x/" + id + "-" + v + ".zip", SHA256: sum, Engine: ">=0.1.0"}
	}
	idx, err := BuildIndex([]RepoEntry{e("b", "1.0.0", "1"), e("a", "1.2.0", "2"), e("a", "1.10.0", "3"), e("a", "1.2.0", "2")})
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Plugins) != 2 || idx.Plugins[0].ID != "a" {
		t.Fatalf("plugins %+v", idx.Plugins)
	}
	a := idx.Plugins[0]
	// semver order (1.10 > 1.2), duplicates collapsed, metadata from the newest.
	if len(a.Versions) != 2 || a.Versions[0].Version != "1.10.0" || a.Name != "a 1.10.0" {
		t.Fatalf("a = %+v", a)
	}
	if _, err := BuildIndex([]RepoEntry{e("a", "1.0.0", "1"), e("a", "1.0.0", "2")}); err == nil {
		t.Fatal("same version with different content must be rejected")
	}
	if _, err := BuildIndex([]RepoEntry{{ID: "a"}}); err == nil {
		t.Fatal("incomplete entry must be rejected")
	}
}

func TestOfficialKeys(t *testing.T) { OfficialKeys() } // panics on a malformed key file
