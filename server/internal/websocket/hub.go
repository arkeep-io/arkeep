package websocket

import (
	"sync"
)

// Hub is the central pub/sub broker for WebSocket clients. It maintains the
// registry of connected clients and routes published messages to all clients
// subscribed to a given topic.
//
// # Design: single-writer event loop
//
// All mutations to the client registry (register, unregister) are serialised
// through a single goroutine — the Run loop — via channels. This eliminates
// the need for a mutex on the registry map and makes the data flow easy to
// reason about. Publish is the one exception: it holds a read-lock for the
// shortest possible time to copy the target set, then sends outside the lock
// to avoid blocking the event loop while waiting on slow client channels.
//
// # Topic format
//
//	job:<uuid>               — updates for a specific backup job
//	agent:<uuid>             — status changes for a specific agent
//	notifications:<user_id>  — in-app notifications for a user
type Hub struct {
	// clients maps each connected client to the set of topics it is
	// subscribed to. Keyed by pointer for O(1) register/unregister.
	clients map[*Client]struct{}

	// topics maps each topic string to the set of clients subscribed to it.
	// Both maps are always updated together to keep them in sync.
	topics map[string]map[*Client]struct{}

	// mu protects clients and topics during Publish, which reads them from
	// outside the Run goroutine. Register and Unregister channels handle
	// writes exclusively inside Run, so no lock is needed there.
	mu sync.RWMutex

	// register receives clients that have just completed the WebSocket
	// upgrade and are ready to receive messages.
	register chan *Client

	// unregister receives clients that have disconnected or encountered a
	// write error. The hub removes them from all topic subscriptions.
	unregister chan *Client

	// stopped is closed when the hub's Run loop exits, signalling that no
	// further messages will be delivered.
	stopped chan struct{}
}

// NewHub creates an idle Hub. Call Run in a goroutine to start it.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		topics:     make(map[string]map[*Client]struct{}),
		register:   make(chan *Client, 16),
		unregister: make(chan *Client, 16),
		stopped:    make(chan struct{}),
	}
}

// Run starts the hub's event loop. It must be called exactly once, in its own
// goroutine. It exits when ctx is cancelled (via server graceful shutdown).
//
//	go hub.Run(ctx)
func (h *Hub) Run(ctx interface{ Done() <-chan struct{} }) {
	defer close(h.stopped)

	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			for _, topic := range client.topics {
				if h.topics[topic] == nil {
					h.topics[topic] = make(map[*Client]struct{})
				}
				h.topics[topic][client] = struct{}{}
			}
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				for _, topic := range client.topics {
					delete(h.topics[topic], client)
					if len(h.topics[topic]) == 0 {
						delete(h.topics, topic)
					}
				}
				// Signal the client's writePump to drain and exit.
				close(client.send)
			}
			h.mu.Unlock()

		case <-ctx.Done():
			// Close all connected clients on shutdown.
			h.mu.Lock()
			for client := range h.clients {
				close(client.send)
			}
			h.clients = make(map[*Client]struct{})
			h.topics = make(map[string]map[*Client]struct{})
			h.mu.Unlock()
			return
		}
	}
}

// Publish sends msg to every client subscribed to topic.
// It is safe to call from any goroutine (scheduler, gRPC handlers, etc.).
// Clients whose send buffer is full are disconnected to prevent backpressure
// from a slow consumer blocking all other subscribers on the same topic.
func (h *Hub) Publish(topic string, msg Message) {
	// Stamp the topic onto the message so the client can route it correctly.
	// Callers do not set Topic themselves — it is always derived from the
	// publish call, so injecting it here keeps all call sites clean.
	msg.Topic = topic

	h.mu.RLock()
	targets := h.topics[topic]
	// Copy the target set before releasing the lock so we don't hold it
	// while sending — channel sends can block if a buffer is full.
	var clients []*Client
	for c := range targets {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		if !trySend(c.send, msg) {
			// Client send buffer is full or the hub closed its channel during
			// shutdown. Queue the client for disconnection without blocking —
			// if the unregister channel is full (hub stopped) we skip silently.
			select {
			case h.unregister <- c:
			default:
			}
		}
	}
}

// trySend attempts a non-blocking send of msg onto ch.
// Returns false if the channel is full or already closed (hub shutdown).
// The recover handles the "send on closed channel" panic that would otherwise
// occur when Publish races with the hub's ctx.Done() shutdown path.
func trySend(ch chan Message, msg Message) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case ch <- msg:
		return true
	default:
		return false
	}
}

// Subscribe registers client with the hub and adds it to all its topics.
// Called by the HTTP upgrade handler after the client is initialised.
func (h *Hub) Subscribe(client *Client) {
	h.register <- client
}

// Unsubscribe removes client from the hub and all its topic subscriptions.
// Called by the client's readPump when the connection closes.
func (h *Hub) Unsubscribe(client *Client) {
	h.unregister <- client
}

// ConnectedCount returns the current number of connected WebSocket clients.
// Intended for metrics and health endpoints.
func (h *Hub) ConnectedCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}