// SabiTun server: a WebSocket endpoint that performs a Noise_IK handshake
// and then relays TCP traffic to whatever target the client asks for
// (like a minimal, authenticated SOCKS-over-Noise-over-WebSocket proxy).
//
// Any request that is not a valid, authenticated tunnel connection gets a
// normal-looking static page, never an abrupt error — so passive/active
// probing sees an ordinary web server.
package main

import (
	"encoding/base64"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/flynn/noise"
	"github.com/gorilla/websocket"

	"github.com/sabi-074/SABI-VIP-OK/sabitun"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// loadOrGenerateServerKey reads the base64 private key from SABITUN_PRIVATE_KEY
// if set, otherwise generates one and logs both keys (for first run / local
// testing only — in production, copy the printed private key into the
// SABITUN_PRIVATE_KEY Railway variable so the identity is stable across
// deploys, and share the printed public key with clients).
func loadOrGenerateServerKey() noise.DHKey {
	if b64 := os.Getenv("SABITUN_PRIVATE_KEY"); b64 != "" {
		priv, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(priv) != 32 {
			log.Fatalf("sabitun: invalid SABITUN_PRIVATE_KEY (must be 32 raw bytes, base64-encoded): %v", err)
		}
		key, err := sabitun.KeypairFromPrivate(priv)
		if err != nil {
			log.Fatalf("sabitun: failed to derive keypair: %v", err)
		}
		log.Printf("SABITUN: loaded static keypair from SABITUN_PRIVATE_KEY. public key: %s",
			base64.StdEncoding.EncodeToString(key.Public))
		return key
	}
	key, err := sabitun.GenerateKeypair()
	if err != nil {
		log.Fatalf("sabitun: keygen failed: %v", err)
	}
	log.Printf("SABITUN: no SABITUN_PRIVATE_KEY set, generated an EPHEMERAL keypair (will change on every restart).")
	log.Printf("SABITUN: public key (share with clients): %s", base64.StdEncoding.EncodeToString(key.Public))
	log.Printf("SABITUN: private key (set as SABITUN_PRIVATE_KEY to persist): %s", base64.StdEncoding.EncodeToString(key.Private))
	return key
}

func tunnelHandler(serverKey noise.DHKey) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// Not a valid WS upgrade at all -> upgrader already wrote a plain
			// response; nothing more to do.
			return
		}
		defer conn.Close()
		conn.SetReadLimit(64 * 1024)

		rw := sabitun.NewWSReadWriter(conn)

		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		sess, _, err := sabitun.RunServerHandshake(rw, serverKey)
		if err != nil {
			// Invalid handshake: close quietly, no error frame, no signal.
			return
		}
		conn.SetReadDeadline(time.Time{})

		handleSession(sess)
	}
}

func handleSession(sess *sabitun.Session) {
	frameType, payload, err := sess.ReadFrame()
	if err != nil {
		return
	}

	switch frameType {
	case sabitun.FrameConnect:
		handleTCP(sess, string(payload))
	case sabitun.FrameUDPAssociate:
		handleUDPAssociate(sess)
	}
}

func handleUDPAssociate(sess *sabitun.Session) {
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return
	}
	defer conn.Close()

	if err := sess.WriteFrame(sabitun.FrameUDPAssociateOK, nil); err != nil {
		return
	}

	done := make(chan struct{}, 2)

	// tunnel -> real UDP target
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			ft, data, err := sess.ReadFrame()
			if err != nil {
				return
			}
			if ft == sabitun.FrameClose {
				return
			}
			if ft != sabitun.FrameUDPSend {
				continue
			}
			addr, payload, err := sabitun.DecodeUDPPacket(data)
			if err != nil {
				continue
			}
			udpAddr, err := net.ResolveUDPAddr("udp", addr)
			if err != nil {
				continue
			}
			_, _ = conn.WriteTo(payload, udpAddr)
		}
	}()

	// real UDP target -> tunnel
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 1400)
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			packet := sabitun.EncodeUDPPacket(from.String(), buf[:n])
			if werr := sess.WriteFrame(sabitun.FrameUDPRecv, packet); werr != nil {
				return
			}
		}
	}()

	<-done
}

func handleTCP(sess *sabitun.Session, target string) {

	dst, err := net.DialTimeout("tcp", target, 8*time.Second)
	if err != nil {
		_ = sess.WriteFrame(sabitun.FrameConnectFail, []byte(err.Error()))
		return
	}
	defer dst.Close()

	if err := sess.WriteFrame(sabitun.FrameConnectOK, nil); err != nil {
		return
	}

	done := make(chan struct{}, 2)

	// tunnel -> destination
	go func() {
		defer func() { done <- struct{}{} }()
		for {
			ft, data, err := sess.ReadFrame()
			if err != nil {
				return
			}
			switch ft {
			case sabitun.FrameData:
				if _, err := dst.Write(data); err != nil {
					return
				}
			case sabitun.FrameClose:
				return
			}
		}
	}()

	// destination -> tunnel
	go func() {
		defer func() { done <- struct{}{} }()
		buf := make([]byte, 1024)
		for {
			n, err := dst.Read(buf)
			if n > 0 {
				if werr := sess.WriteFrame(sabitun.FrameData, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = sess.WriteFrame(sabitun.FrameClose, nil)
				return
			}
		}
	}()

	<-done
}

const fallbackHTML = `<!doctype html><html><head><title>OK</title></head>
<body><p>It works.</p></body></html>`

func fallbackHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fallbackHTML))
}

func main() {
	serverKey := loadOrGenerateServerKey()

	mux := http.NewServeMux()
	mux.HandleFunc("/connect", tunnelHandler(serverKey))
	mux.HandleFunc("/", fallbackHandler)

	// This binary always listens on a fixed LOOPBACK-ONLY port in the
	// SABI-VIP-OK all-in-one container; nginx is the single public-facing
	// process (on Railway's $PORT) and reverse-proxies /connect here. This
	// keeps the same probe-resistance behavior (nginx's own catch-all /
	// serves the boring fallback page) while letting one container run
	// SabiTun + 4 other protocols side by side.
	addr := os.Getenv("SABITUN_LOCAL_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8081"
	}
	log.Printf("SABITUN: listening on %s (internal only, behind nginx)", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
