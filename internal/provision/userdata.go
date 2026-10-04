// Package provision creates and destroys Exits on a cloud provider.
package provision

import (
	"bytes"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/bontaramsonta/poof/internal/wgkey"
)

// ExitParams are the values baked into an Exit's cloud-init at launch.
type ExitParams struct {
	// ServerPrivate is generated locally and handed to the Exit here;
	// it never leaves this launch request.
	ServerPrivate wgkey.Key
	// ClientPublic is the only peer the Exit will ever accept.
	ClientPublic wgkey.Key

	ServerTunnelIP string // e.g. 10.66.0.1
	ClientTunnelIP string // e.g. 10.66.0.2
	ListenPort     int    // 51820

	// IdleShutdownMin is how long the Exit tolerates handshake silence
	// before self-destructing (ADR-0003).
	IdleShutdownMin int

	// PhoneTest replaces the Dead-man's switch with a hard stop after
	// PhoneTestHardStopMin and logs the handshake age to the serial
	// console every 30 s. Debug only: measures how long a sleeping phone
	// stays silent (poof-android#12).
	PhoneTest bool
}

// PhoneTestHardStopMin bounds a PhoneTest Exit's life, since it has no
// Dead-man's switch.
const PhoneTestHardStopMin = 120

// PhoneTestLogPrefix marks handshake-age lines on the serial console.
const PhoneTestLogPrefix = "POOFHS"

//go:embed userdata.sh.tmpl
var userDataTmplSrc string

// userDataTmpl is the cloud-init script that turns a blank Amazon Linux
// 2023 box into an Exit. It runs once, as root, at first boot. There is
// no SSH: if this script is wrong, debug via the EC2 serial console.
// The script itself lives in userdata.sh.tmpl, next to this file.
var userDataTmpl = template.Must(template.New("userdata").Parse(userDataTmplSrc))

// RenderUserData produces the cloud-init script for the given Exit.
func RenderUserData(p ExitParams) (string, error) {
	if p.IdleShutdownMin <= 0 {
		return "", fmt.Errorf("provision: IdleShutdownMin must be positive")
	}
	data := struct {
		ExitParams
		ServerPrivateB64 string
		ClientPublicB64  string
		HardStopMin      int
		LogPrefix        string
	}{
		ExitParams:       p,
		ServerPrivateB64: p.ServerPrivate.Base64(),
		ClientPublicB64:  p.ClientPublic.Base64(),
		HardStopMin:      PhoneTestHardStopMin,
		LogPrefix:        PhoneTestLogPrefix,
	}
	var buf bytes.Buffer
	if err := userDataTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("provision: rendering user-data: %w", err)
	}
	return buf.String(), nil
}
