package tunnel

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/device"
)

// mtu is 1500 (ethernet) minus WireGuard's worst-case 80-byte overhead.
const mtu = 1420

// keepaliveSec keeps NAT mappings warm and, because it forces the ~2min
// re-handshake cadence even when idle, feeds the Exit's dead-man's
// switch its "client still here" signal (ADR-0003).
const keepaliveSec = 25

// buildIPC renders the UAPI config both backends configure the engine
// with. allowed_ip=0.0.0.0/0 routes everything to the Exit.
func buildIPC(cfg Config) string {
	return fmt.Sprintf(
		"private_key=%s\n"+
			"public_key=%s\n"+
			"endpoint=%s\n"+
			"allowed_ip=0.0.0.0/0\n"+
			"persistent_keepalive_interval=%d\n",
		cfg.PrivateKey.Hex(), cfg.PeerPublic.Hex(), cfg.Endpoint, keepaliveSec)
}

func deviceLogLevel(verbose bool) int {
	if verbose {
		return device.LogLevelVerbose
	}
	return device.LogLevelError
}

// parseStatus extracts liveness/throughput from the device's IPC state
// (the same key=value text `wg show` renders).
func parseStatus(raw string) Status {
	var s Status
	var hsSec, hsNsec int64
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "last_handshake_time_sec":
			hsSec, _ = strconv.ParseInt(v, 10, 64)
		case "last_handshake_time_nsec":
			hsNsec, _ = strconv.ParseInt(v, 10, 64)
		case "rx_bytes":
			s.RxBytes, _ = strconv.ParseUint(v, 10, 64)
		case "tx_bytes":
			s.TxBytes, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	if hsSec > 0 {
		s.LastHandshake = time.Unix(hsSec, hsNsec)
	}
	return s
}

// waitForHandshake polls status until the peer completes a handshake (the
// keepalive triggers one immediately on Up) or ctx expires. This is the
// "is the Exit alive yet?" poll the provisioner relies on.
func waitForHandshake(ctx context.Context, status func() (Status, error)) error {
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		s, err := status()
		if err != nil {
			return err
		}
		if !s.LastHandshake.IsZero() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("tunnel: no handshake from peer: %w", ctx.Err())
		case <-tick.C:
		}
	}
}
