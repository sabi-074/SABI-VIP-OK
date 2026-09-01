package sabitun

import (
	"golang.org/x/crypto/curve25519"

	"github.com/flynn/noise"
)

// KeypairFromPrivate derives the full DHKey (private+public) from a raw
// 32-byte X25519 private scalar, e.g. one loaded from an env var so the
// server's identity is stable across redeploys.
func KeypairFromPrivate(priv []byte) (noise.DHKey, error) {
	var pub [32]byte
	var p [32]byte
	copy(p[:], priv)
	curve25519.ScalarBaseMult(&pub, &p)
	return noise.DHKey{Private: priv, Public: pub[:]}, nil
}
