// Package sabitun implements the SabiTun tunnel protocol: a Noise_IK
// handshake carried over WebSocket, with padded, encrypted framing on top.
//
// This is a research/testing protocol. It has NOT been audited. Use at your
// own risk.
package sabitun

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"

	"github.com/flynn/noise"
)

// Frame types, carried as the first byte of the Noise plaintext payload.
const (
	FrameConnect     byte = 0x01 // client -> server: "connect to this target"
	FrameConnectOK   byte = 0x02 // server -> client: connect succeeded
	FrameConnectFail byte = 0x03 // server -> client: connect failed
	FrameData        byte = 0x04 // either direction: relayed payload bytes
	FrameClose       byte = 0x05 // either direction: close this stream

	FrameUDPAssociate   byte = 0x06 // client -> server: "open a UDP relay" (no fixed target)
	FrameUDPAssociateOK byte = 0x07 // server -> client: UDP relay ready
	FrameUDPSend        byte = 0x08 // client -> server: one UDP datagram to relay out
	FrameUDPRecv        byte = 0x09 // server -> client: one UDP datagram received back
)

// paddedBuckets defines the plaintext-frame size buckets used to reduce
// length-based traffic fingerprinting. Real content is length-prefixed
// (2 bytes) inside the bucket; the rest is random padding.
var paddedBuckets = []int{128, 256, 512, 1024, 1400}

func bucketFor(n int) (int, error) {
	for _, b := range paddedBuckets {
		if n <= b-2 { // 2 bytes reserved for the length prefix
			return b, nil
		}
	}
	return 0, errors.New("sabitun: payload too large for any padding bucket")
}

// EncodePadded builds a padded plaintext frame: [type byte][2-byte len][payload][random pad].
func EncodePadded(frameType byte, payload []byte) ([]byte, error) {
	inner := make([]byte, 1+len(payload))
	inner[0] = frameType
	copy(inner[1:], payload)

	bucket, err := bucketFor(len(inner))
	if err != nil {
		return nil, err
	}
	out := make([]byte, bucket)
	binary.BigEndian.PutUint16(out[:2], uint16(len(inner)))
	copy(out[2:], inner)
	if _, err := rand.Read(out[2+len(inner):]); err != nil {
		return nil, err
	}
	return out, nil
}

// DecodePadded reverses EncodePadded, returning the frame type and payload.
func DecodePadded(buf []byte) (byte, []byte, error) {
	if len(buf) < 3 {
		return 0, nil, errors.New("sabitun: frame too short")
	}
	n := int(binary.BigEndian.Uint16(buf[:2]))
	if n < 1 || 2+n > len(buf) {
		return 0, nil, errors.New("sabitun: corrupt frame length")
	}
	inner := buf[2 : 2+n]
	return inner[0], inner[1:], nil
}

// NoiseConfig bundles the cipher suite used throughout SabiTun: Noise_IK,
// X25519, ChaCha20-Poly1305, BLAKE2s. These are the same primitive family
// WireGuard uses; we are not inventing new cryptography, only a new
// transport/obfuscation layer on top of it.
var CipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2s)

// NewHandshakeState builds a Noise_IK handshake state for either role.
//
// initiator: true for the client, false for the server.
// staticKey: this side's static keypair (required on both sides for IK).
// peerStatic: the remote party's static public key. On the client this is
// the server's pinned public key. On the server this is nil (the server
// learns the client's key during the handshake — SabiTun does not
// authenticate clients by static key, only the server is pinned).
func NewHandshakeState(initiator bool, staticKey noise.DHKey, peerStatic []byte) (*noise.HandshakeState, error) {
	return noise.NewHandshakeState(noise.Config{
		CipherSuite:   CipherSuite,
		Pattern:       noise.HandshakeIK,
		Initiator:     initiator,
		StaticKeypair: staticKey,
		PeerStatic:    peerStatic,
	})
}

// GenerateKeypair creates a new static X25519 keypair for the suite above.
func GenerateKeypair() (noise.DHKey, error) {
	return CipherSuite.GenerateKeypair(rand.Reader)
}

// WriteFrame Noise-encrypts a padded frame and writes it as a single
// length-prefixed message on w (used for the CLI/relay transport under
// WebSocket abstractions this is bypassed — WS already frames messages).
func WriteFrame(w io.Writer, cs *noise.CipherState, frameType byte, payload []byte) error {
	padded, err := EncodePadded(frameType, payload)
	if err != nil {
		return err
	}
	ct, err := cs.Encrypt(nil, nil, padded)
	if err != nil {
		return err
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(ct)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = w.Write(ct)
	return err
}

// ReadFrame reads and decrypts one frame written by WriteFrame.
func ReadFrame(r io.Reader, cs *noise.CipherState) (byte, []byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n > 16*1024 {
		return 0, nil, errors.New("sabitun: frame too large")
	}
	ct := make([]byte, n)
	if _, err := io.ReadFull(r, ct); err != nil {
		return 0, nil, err
	}
	pt, err := cs.Decrypt(nil, nil, ct)
	if err != nil {
		return 0, nil, err
	}
	return DecodePadded(pt)
}


// EncodeUDPPacket packs a target/source address and a UDP payload into one
// frame payload: [1-byte addrLen][addr bytes]["host:port"][data...].
func EncodeUDPPacket(addr string, data []byte) []byte {
	b := make([]byte, 1+len(addr)+len(data))
	b[0] = byte(len(addr))
	copy(b[1:], addr)
	copy(b[1+len(addr):], data)
	return b
}

// DecodeUDPPacket reverses EncodeUDPPacket.
func DecodeUDPPacket(b []byte) (addr string, data []byte, err error) {
	if len(b) < 1 {
		return "", nil, errors.New("sabitun: empty UDP packet")
	}
	n := int(b[0])
	if 1+n > len(b) {
		return "", nil, errors.New("sabitun: corrupt UDP packet")
	}
	return string(b[1 : 1+n]), b[1+n:], nil
}
