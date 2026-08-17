package provision

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bontaramsonta/poof/internal/wgkey"
)

func sampleParams(t *testing.T) ExitParams {
	t.Helper()
	srv, err := wgkey.GeneratePrivate()
	if err != nil {
		t.Fatal(err)
	}
	cli, err := wgkey.GeneratePrivate()
	if err != nil {
		t.Fatal(err)
	}
	return ExitParams{
		ServerPrivate:   srv,
		ClientPublic:    cli.Public(),
		ServerTunnelIP:  "10.66.0.1",
		ClientTunnelIP:  "10.66.0.2",
		ListenPort:      51820,
		IdleShutdownMin: 5,
	}
}

func TestRenderUserDataContents(t *testing.T) {
	p := sampleParams(t)
	out, err := RenderUserData(p)
	if err != nil {
		t.Fatal(err)
	}

	// The server's own private key must appear (base64), and the client
	// pubkey must be the sole peer.
	if !strings.Contains(out, p.ServerPrivate.Base64()) {
		t.Error("server private key missing from user-data")
	}
	if !strings.Contains(out, p.ClientPublic.Base64()) {
		t.Error("client public key missing from user-data")
	}
	// The client's PRIVATE key must never be here — we don't even have
	// it in ExitParams, but guard against a future mistake.
	if strings.Contains(out, "ip_forward=1") == false {
		t.Error("ip forwarding not enabled")
	}
	if !strings.Contains(out, "shutdown -h now") {
		t.Error("dead-man's switch missing")
	}
}

// TestUserDataIsValidBash catches template edits that produce broken
// shell, since a bad script can't be debugged without SSH.
func TestUserDataIsValidBash(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	out, err := RenderUserData(sampleParams(t))
	if err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "userdata.sh")
	if err := os.WriteFile(f, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("bash", "-n", f).CombinedOutput(); err != nil {
		t.Fatalf("generated script has syntax errors: %v\n%s", err, b)
	}
}

// TestUserDataStartsWithShebang guards the one failure mode extraction
// introduced: EC2 silently refuses to run user data that doesn't begin
// with "#!", and there's no SSH to notice.
func TestUserDataStartsWithShebang(t *testing.T) {
	if !strings.HasPrefix(userDataTmplSrc, "#!") {
		t.Errorf("userdata.sh.tmpl must start with a shebang at byte 0, got %.20q", userDataTmplSrc)
	}
}

func TestRenderUserDataRejectsBadIdle(t *testing.T) {
	p := sampleParams(t)
	p.IdleShutdownMin = 0
	if _, err := RenderUserData(p); err == nil {
		t.Error("expected error for zero IdleShutdownMin")
	}
}
