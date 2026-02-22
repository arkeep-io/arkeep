package websocket

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	// writeWait is the maximum time allowed to write a message to the peer.
	// If the write does not complete within this window the connection is
	// closed — this prevents a stalled client from blocking the writePump.
	writeWait = 10 * time.Second

	// pongWait is how long the server waits for a pong reply after sending
	// a ping. The connection is closed if no pong arrives in time.
	pongWait = 60 * time.Second

	// pingPeriod is how often the server sends a ping frame to the client.
	// Must be less than pongWait so the client has time to reply.
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize is the maximum size in bytes accepted from the client.
	// Clients only send close/pong frames — a small limit is sufficient.
	maxMessageSize = 512

	// sendBufferSize is the capacity of the per-client message channel.
	// If the buffer fills up the client is considered too slow and is
	// disconnected by Publish to prevent backpressure on other subscribers.
	sendBufferSize = 32
)

// upgrader performs the HTTP → WebSocket protocol upgrade.
// CheckOrigin always returns true — origin validation is the responsibility
// of the reverse proxy (nginx, Caddy) in production deployments.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Client represents a single connected WebSocket peer. Each client runs two
// goroutines: readPump (detects disconnection, handles pong frames) and
// writePump (serialises outgoing messages onto the wire).
//
// The send channel is the handoff point between the hub's Publish calls and
// the writePump. It is closed by the hub when the client is unregistered,
// which causes writePump to drain and exit cleanly.
type Client struct {
	// hub is the parent hub that manages this client's lifecycle.
	hub *Hub

	// conn is the underlying WebSocket connection.
	conn *websocket.Conn

	// send is the outbound message buffer. The hub writes here; writePump
	// reads from here and forwards to the wire.
	send chan Message

	// topics is the set of pub/sub topics this client is subscribed to.
	// Populated once at connection time from query parameters and the JWT
	// claims (notifications topic is always added automatically).
	// Read-only after initialisation — no synchronisation needed.
	topics []string

	// logger is a scoped zap logger with the remote address pre-filled.
	logger *zap.Logger
}

// NewClient creates a Client and upgrades the HTTP connection to WebSocket.
// topics is the list of pub/sub channels the client wants to receive.
// The notifications:<user_id> topic must be included by the caller.
//
// Returns an error if the upgrade fails (e.g. the request is not a valid
// WebSocket handshake).
func NewClient(hub *Hub, w http.ResponseWriter, r *http.Request, topics []string, logger *zap.Logger) (*Client, error) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return nil, err
	}

	c := &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan Message, sendBufferSize),
		topics: topics,
		logger: logger.With(zap.String("remote_addr", r.RemoteAddr)),
	}
	return c, nil
}

// Run registers the client with the hub and starts the read and write pumps.
// It blocks until the connection closes. The caller should invoke it in a
// goroutine if they need to return from the HTTP handler immediately —
// however, since this is called from an HTTP handler that has already
// completed the upgrade, blocking is fine.
func (c *Client) Run() {
	c.hub.Subscribe(c)

	// writePump runs in a separate goroutine because it blocks on the send
	// channel and the wire write. readPump runs on the current goroutine.
	go c.writePump()
	c.readPump()
}

// readPump reads incoming frames from the WebSocket connection. Its primary
// job is to detect client disconnection and reset the read deadline after
// each pong frame. Actual application messages from the client are not
// expected — the protocol is server-push only.
//
// When the loop exits (connection closed or error), the client is unregistered
// from the hub so resources are freed.
func (c *Client) readPump() {
	defer func() {
		c.hub.Unsubscribe(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)

	// Set the initial read deadline. The deadline is reset on every pong.
	if err := c.conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		c.logger.Warn("ws: failed to set read deadline", zap.Error(err))
		return
	}

	c.conn.SetPongHandler(func(string) error {
		// Reset the deadline each time a pong arrives so the connection
		// stays alive as long as the client is responsive.
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		// ReadMessage blocks until a frame arrives or the deadline expires.
		// We discard the message content — clients only send pong frames.
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseNormalClosure,
				websocket.CloseNoStatusReceived,
			) {
				c.logger.Warn("ws: unexpected close", zap.Error(err))
			}
			return
		}
	}
}

// writePump forwards messages from the send channel to the WebSocket wire.
// It also sends periodic ping frames so readPump can detect stale connections.
//
// writePump is the only goroutine that writes to conn — gorilla/websocket
// connections are not safe for concurrent writes.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.logger.Warn("ws: failed to set write deadline", zap.Error(err))
				return
			}

			if !ok {
				// The hub closed the channel — send a close frame and exit.
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteJSON(msg); err != nil {
				c.logger.Warn("ws: write error", zap.Error(err))
				return
			}

		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				c.logger.Warn("ws: failed to set write deadline", zap.Error(err))
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.logger.Warn("ws: ping error", zap.Error(err))
				return
			}
		}
	}
}