// Package provision creates and destroys Exits on a cloud provider.
package provision

import (
	"bytes"
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
}

// userDataTmpl is the cloud-init script that turns a blank Amazon Linux
// 2023 box into an Exit. It runs once, as root, at first boot. There is
// no SSH: if this script is wrong, debug via the EC2 serial console.
var userDataTmpl = template.Must(template.New("userdata").Parse(`#!/bin/bash
set -euxo pipefail

# 1. Kernel WireGuard (the server has root and wants raw throughput).
dnf install -y wireguard-tools

# 2. The interface config: our server key, and exactly one peer — the
#    client whose keypair was generated for this session.
umask 077
mkdir -p /etc/wireguard
cat >/etc/wireguard/wg0.conf <<'EOF'
[Interface]
Address = {{.ServerTunnelIP}}/24
ListenPort = {{.ListenPort}}
PrivateKey = {{.ServerPrivateB64}}

[Peer]
PublicKey = {{.ClientPublicB64}}
AllowedIPs = {{.ClientTunnelIP}}/32
EOF

# 3. Become a router: forward tunnel traffic out the primary NIC and
#    masquerade it behind this box's public IP.
echo 'net.ipv4.ip_forward=1' >/etc/sysctl.d/99-poof.conf
sysctl -p /etc/sysctl.d/99-poof.conf
PRIMARY_IF=$(ip -o -4 route show to default | awk '{print $5}')
iptables -t nat -A POSTROUTING -o "$PRIMARY_IF" -j MASQUERADE
iptables -A FORWARD -i wg0 -o "$PRIMARY_IF" -j ACCEPT
iptables -A FORWARD -i "$PRIMARY_IF" -o wg0 -m state --state RELATED,ESTABLISHED -j ACCEPT

# 4. Bring the tunnel up now and on every boot.
systemctl enable --now wg-quick@wg0

# 5. Dead-man's switch: if no handshake for {{.IdleShutdownMin}} min,
#    self-destruct. shutdown == terminate (set at launch time).
cat >/usr/local/bin/poof-deadman.sh <<'EOF'
#!/bin/bash
threshold=$(( {{.IdleShutdownMin}} * 60 ))
now=$(date +%s)
newest=0
while read -r _peer ts; do
  [ "$ts" -gt "$newest" ] && newest=$ts
done < <(wg show wg0 latest-handshakes)
# Grace period: if no handshake has EVER happened yet, use boot time so
# a slow first connection doesn't kill a fresh box.
if [ "$newest" -eq 0 ]; then
  newest=$(( now - $(cut -d. -f1 /proc/uptime) ))
fi
if [ $(( now - newest )) -gt "$threshold" ]; then
  logger -t poof "no handshake for >${threshold}s; self-destructing"
  shutdown -h now
fi
EOF
chmod +x /usr/local/bin/poof-deadman.sh

cat >/etc/systemd/system/poof-deadman.service <<'EOF'
[Unit]
Description=poof dead-man's switch
[Service]
Type=oneshot
ExecStart=/usr/local/bin/poof-deadman.sh
EOF
cat >/etc/systemd/system/poof-deadman.timer <<'EOF'
[Unit]
Description=run poof dead-man's switch every minute
[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
[Install]
WantedBy=timers.target
EOF
systemctl enable --now poof-deadman.timer
`))

// RenderUserData produces the cloud-init script for the given Exit.
func RenderUserData(p ExitParams) (string, error) {
	if p.IdleShutdownMin <= 0 {
		return "", fmt.Errorf("provision: IdleShutdownMin must be positive")
	}
	data := struct {
		ExitParams
		ServerPrivateB64 string
		ClientPublicB64  string
	}{
		ExitParams:       p,
		ServerPrivateB64: p.ServerPrivate.Base64(),
		ClientPublicB64:  p.ClientPublic.Base64(),
	}
	var buf bytes.Buffer
	if err := userDataTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("provision: rendering user-data: %w", err)
	}
	return buf.String(), nil
}
