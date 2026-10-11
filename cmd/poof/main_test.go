package main

import "testing"

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		n    uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{5 * 1024 * 1024, "5.0 MiB"},
		{1073741824, "1.0 GiB"},
	}
	for _, c := range cases {
		if got := humanBytes(c.n); got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestParseUpArgs(t *testing.T) {
	got, err := parseUpArgs([]string{"--only", "API.groww.in., x.com", "india", "--static-ip", "groww"})
	if err != nil {
		t.Fatal(err)
	}
	if got.country != "india" || got.staticIP != "groww" || len(got.only) != 2 ||
		got.only[0] != "api.groww.in" || got.only[1] != "x.com" {
		t.Fatalf("got %+v", got)
	}
	for _, bad := range [][]string{
		{},
		{"india", "--static-ip"},
		{"india", "--static-ip", "Bad Name"},
		{"--system", "--only", "x.com", "india"},
		{"india", "japan"},
		{"--bogus", "india"},
	} {
		if _, err := parseUpArgs(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}
