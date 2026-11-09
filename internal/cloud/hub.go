package cloud

import (
	"errors"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

func NewHubSession(conn *websocket.Conn) (*yamux.Session, error) {
	if conn == nil {
		return nil, errors.New("cloud: WebSocket connection is required")
	}
	config := tunnelYamuxConfig()
	config.StreamOpenTimeout = 0
	config.StreamCloseTimeout = time.Second
	session, err := yamux.Client(newWSConn(conn), config)
	if err != nil {
		_ = conn.Close()
		return nil, errors.New("cloud: yamux client setup failed")
	}
	return session, nil
}
