package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Abaddollyon/spectr/pkg/engine"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Permissive for development; tighten in production with origin checks.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsEnvelope wraps a streamed payload with a type discriminator.
type wsEnvelope struct {
	Type      string        `json:"type"`
	Payload   *engine.Event `json:"payload"`
	Timestamp time.Time     `json:"timestamp"`
}

// client represents a single connected WebSocket peer.
type client struct {
	hub  *Hub
	conn *websocket.Conn
	send chan []byte // buffered outbound messages
}

// Hub manages all active WebSocket connections and fan-out broadcasting.
type Hub struct {
	mu         sync.RWMutex
	clients    map[*client]struct{}
	broadcast  chan []byte
	register   chan *client
	unregister chan *client
}

func newHub() *Hub {
	return &Hub{
		clients:    make(map[*client]struct{}),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *client, 32),
		unregister: make(chan *client, 32),
	}
}

// run is the hub's central event loop. Must be called in a goroutine.
func (h *Hub) run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Slow consumer: drop message rather than block.
				}
			}
			h.mu.RUnlock()
		}
	}
}

// BroadcastEvent serialises an event and fans it out to all connected clients.
// Safe to call from any goroutine.
func (h *Hub) BroadcastEvent(event *engine.Event) error {
	env := wsEnvelope{
		Type:      "event",
		Payload:   event,
		Timestamp: time.Now().UTC(),
	}
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	h.broadcast <- b
	return nil
}

// ConnectedClients returns the current number of connected WebSocket clients.
func (h *Hub) ConnectedClients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// handleWebSocket upgrades the HTTP connection to WebSocket and registers
// the new client with the hub.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// upgrader already writes an HTTP error response on failure.
		return
	}

	c := &client{
		hub:  s.hub,
		conn: conn,
		send: make(chan []byte, 64),
	}
	s.hub.register <- c

	// Each client needs two goroutines: one writer, one reader (for control frames).
	go c.writePump()
	go c.readPump()
}

// writePump drains the client's send channel and writes messages to the wire.
func (c *client) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// readPump reads incoming frames (ping/pong/close) and initiates cleanup on disconnect.
func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second)) //nolint:errcheck
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}
