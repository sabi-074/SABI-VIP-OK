package sabitun

import (
	"io"

	"github.com/gorilla/websocket"
)

// WSReadWriter adapts a *websocket.Conn to io.ReadWriter using binary
// messages, so the rest of the protocol code doesn't need to know about
// WebSocket framing at all.
type WSReadWriter struct {
	conn    *websocket.Conn
	readBuf []byte
}

func NewWSReadWriter(conn *websocket.Conn) *WSReadWriter {
	return &WSReadWriter{conn: conn}
}

func (w *WSReadWriter) Write(p []byte) (int, error) {
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *WSReadWriter) Read(p []byte) (int, error) {
	for len(w.readBuf) == 0 {
		_, msg, err := w.conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		w.readBuf = msg
	}
	n := copy(p, w.readBuf)
	w.readBuf = w.readBuf[n:]
	return n, nil
}

var _ io.ReadWriter = (*WSReadWriter)(nil)
