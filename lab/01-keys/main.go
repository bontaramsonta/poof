// Lab 01: a WireGuard identity is nothing but a Curve25519 keypair.
// Generate one and print it in the base64 form used by wg(8) configs.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/curve25519"
)

func main() {
	// A private key is 32 random bytes...
	var priv [32]byte
	if _, err := rand.Read(priv[:]); err != nil {
		panic(err)
	}

	// ...with three bits "clamped" per the Curve25519 spec, so every
	// 32-byte string maps onto a valid, safe scalar (no subgroup leaks).
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64

	// The public key is the scalar multiplied by the curve's base point.
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		panic(err)
	}

	fmt.Println("private:", base64.StdEncoding.EncodeToString(priv[:]))
	fmt.Println("public: ", base64.StdEncoding.EncodeToString(pub))
}
