// Package wgkey implements WireGuard identities: Curve25519 keypairs.
package wgkey

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

// Key is a WireGuard key, private or public. It deliberately has no
// String method: keys must be serialized explicitly via Hex or Base64,
// never by accident in a log line.
type Key [32]byte

// GeneratePrivate returns a fresh private key: 32 random bytes with the
// three Curve25519 clamp bits set.
func GeneratePrivate() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, fmt.Errorf("wgkey: reading randomness: %w", err)
	}
	k[0] &= 248
	k[31] &= 127
	k[31] |= 64
	return k, nil
}

// Public derives the public key for a private key.
func (k Key) Public() Key {
	pub, err := curve25519.X25519(k[:], curve25519.Basepoint)
	if err != nil {
		// Only reachable if the private key is the all-zero point;
		// GeneratePrivate cannot produce one.
		panic("wgkey: deriving public key: " + err.Error())
	}
	var out Key
	copy(out[:], pub)
	return out
}

// Hex is the encoding the wireguard-go IPC/UAPI interface expects.
func (k Key) Hex() string { return hex.EncodeToString(k[:]) }

// Base64 is the encoding wg(8) config files and tools expect.
func (k Key) Base64() string { return base64.StdEncoding.EncodeToString(k[:]) }
