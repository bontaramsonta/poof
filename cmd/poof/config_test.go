package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	cfg, err := parseConfig(strings.NewReader("# who I am\n\n  owner =  sourav  \n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg["owner"] != "sourav" {
		t.Fatalf("owner = %q", cfg["owner"])
	}
	for _, bad := range []string{"owner sourav\n", "ownr = sourav\n"} {
		if _, err := parseConfig(strings.NewReader(bad)); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestResolveOwner(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	set := filepath.Join(dir, "set")
	if err := os.WriteFile(set, []byte("owner = sourav\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("# nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		flag, path, want string
	}{
		{"", set, "sourav"},
		{"vaisnav", set, "vaisnav"},
		{"vaisnav", missing, "vaisnav"},
	}
	for _, c := range cases {
		got, err := resolveOwner(c.flag, c.path)
		if err != nil || got != c.want {
			t.Errorf("resolveOwner(%q, %s) = %q, %v; want %q", c.flag, filepath.Base(c.path), got, err, c.want)
		}
	}
	for _, path := range []string{missing, empty} {
		if _, err := resolveOwner("", path); err == nil || !strings.Contains(err.Error(), "no owner set") {
			t.Errorf("%s: err = %v, want refusal", filepath.Base(path), err)
		}
	}
	if _, err := resolveOwner("Bad Owner", set); err == nil {
		t.Error("invalid --owner: want error")
	}
}
