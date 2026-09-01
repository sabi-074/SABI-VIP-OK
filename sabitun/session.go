package sabitun

import (
	"errors"
	"io"

	"github.com/flynn/noise"
)

// Session wraps a completed Noise_IK handshake into a simple send/recv API
// with independent cipher states for each direction, and provides the
// relay loop used to pump bytes between a local net.Conn and the tunnel.
type Session struct {
	send *noise.CipherState
	recv *noise.CipherState
	rw   io.ReadWriter // underlying transport (e.g. a websocket.Conn wrapped as io.ReadWriter)
}

func NewSession(rw io.ReadWriter, send, recv *noise.CipherState) *Session {
	return &Session{send: send, recv: recv, rw: rw}
}

func (s *Session) WriteFrame(frameType byte, payload []byte) error {
	return WriteFrame(s.rw, s.send, frameType, payload)
}

func (s *Session) ReadFrame() (byte, []byte, error) {
	return ReadFrame(s.rw, s.recv)
}

// RunClientHandshake performs the IK handshake as the initiator (client)
// over rw (2 messages: -> e, es, s, ss  then <- e, ee, se) and returns a
// ready Session.
func RunClientHandshake(rw io.ReadWriter, clientKey noise.DHKey, serverPub []byte) (*Session, error) {
	hs, err := NewHandshakeState(true, clientKey, serverPub)
	if err != nil {
		return nil, err
	}
	msg1, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, err
	}
	if err := writeRaw(rw, msg1); err != nil {
		return nil, err
	}
	msg2, err := readRaw(rw)
	if err != nil {
		return nil, err
	}
	_, csSend, csRecv, err := hs.ReadMessage(nil, msg2)
	if err != nil {
		return nil, err
	}
	if csSend == nil || csRecv == nil {
		return nil, errors.New("sabitun: handshake did not complete")
	}
	return NewSession(rw, csSend, csRecv), nil
}

// RunServerHandshake performs the IK handshake as the responder (server).
func RunServerHandshake(rw io.ReadWriter, serverKey noise.DHKey) (*Session, []byte, error) {
	hs, err := NewHandshakeState(false, serverKey, nil)
	if err != nil {
		return nil, nil, err
	}
	msg1, err := readRaw(rw)
	if err != nil {
		return nil, nil, err
	}
	_, _, _, err = hs.ReadMessage(nil, msg1)
	if err != nil {
		return nil, nil, err
	}
	msg2, csRecv, csSend, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, nil, err
	}
	if err := writeRaw(rw, msg2); err != nil {
		return nil, nil, err
	}
	if csSend == nil || csRecv == nil {
		return nil, nil, errors.New("sabitun: handshake did not complete")
	}
	clientPub := hs.PeerStatic()
	return NewSession(rw, csSend, csRecv), clientPub, nil
}

func writeRaw(rw io.ReadWriter, b []byte) error {
	var lenBuf [4]byte
	lenBuf[0] = byte(len(b) >> 24)
	lenBuf[1] = byte(len(b) >> 16)
	lenBuf[2] = byte(len(b) >> 8)
	lenBuf[3] = byte(len(b))
	if _, err := rw.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := rw.Write(b)
	return err
}

func readRaw(rw io.ReadWriter) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(rw, lenBuf[:]); err != nil {
		return nil, err
	}
	n := int(lenBuf[0])<<24 | int(lenBuf[1])<<16 | int(lenBuf[2])<<8 | int(lenBuf[3])
	if n < 0 || n > 8192 {
		return nil, errors.New("sabitun: bad handshake message length")
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(rw, buf)
	return buf, err
}
