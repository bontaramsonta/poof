package wgkey

import "testing"

func TestParseBase64RoundTrip(t *testing.T) {
	priv, err := GeneratePrivate()
	if err != nil {
		t.Fatal(err)
	}
	pub := priv.Public()
	got, err := ParseBase64(pub.Base64())
	if err != nil {
		t.Fatal(err)
	}
	if got != pub {
		t.Fatal("round trip changed the key")
	}
}

func TestParseBase64Rejects(t *testing.T) {
	for _, s := range []string{"", "not base64!", "AAAA"} {
		if _, err := ParseBase64(s); err == nil {
			t.Errorf("ParseBase64(%q) succeeded, want error", s)
		}
	}
}
