package routes

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dadyprojects/quick/db"
	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type client struct {
	id   string
	conn *websocket.Conn
	send chan []byte
}

type room struct {
	clients map[string]*client
}

// Hub manages WebSocket rooms keyed by site_id:room
type Hub struct {
	mu    sync.RWMutex
	rooms map[string]*room
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[string]*room)}
}

func makeRoomKey(siteID int64, roomName string) string {
	return fmt.Sprintf("%d:%s", siteID, roomName)
}

func (h *Hub) join(key, clientID string, conn *websocket.Conn) *client {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[key]
	if !ok {
		r = &room{clients: make(map[string]*client)}
		h.rooms[key] = r
	}
	cl := &client{
		id:   clientID,
		conn: conn,
		send: make(chan []byte, 64),
	}
	r.clients[clientID] = cl
	return cl
}

func (h *Hub) leave(key, clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[key]
	if !ok {
		return
	}
	if cl, ok := r.clients[clientID]; ok {
		close(cl.send)
		delete(r.clients, clientID)
	}
	if len(r.clients) == 0 {
		delete(h.rooms, key)
	}
}

func (h *Hub) broadcast(key, fromID string, payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r, ok := h.rooms[key]
	if !ok {
		return
	}
	for id, cl := range r.clients {
		if id == fromID {
			continue // do not echo to sender
		}
		select {
		case cl.send <- payload:
		default:
			// slow client: drop message
		}
	}
}

// WSUpgrade rejects non-websocket requests.
func WSUpgrade(c *fiber.Ctx) error {
	if websocket.IsWebSocketUpgrade(c) {
		return c.Next()
	}
	return fiber.ErrUpgradeRequired
}

// HandleWS is the websocket handler (use with websocket.New).
func (h *Hub) HandleWS(c *websocket.Conn) {
	siteVal := c.Locals("site")
	if siteVal == nil {
		_ = c.WriteJSON(map[string]any{"type": "error", "data": "site not found"})
		_ = c.Close()
		return
	}
	site := siteVal.(*db.Site)
	roomName := c.Query("room")
	if roomName == "" {
		roomName = "default"
	}
	if len(roomName) > 128 {
		_ = c.WriteJSON(map[string]any{"type": "error", "data": "room name too long"})
		_ = c.Close()
		return
	}

	key := makeRoomKey(site.ID, roomName)
	clientID := uuid.New().String()
	cl := h.join(key, clientID, c)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range cl.send {
			_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
	}()

	// Explicit cleanup order: stop accepting → leave (closes send) → wait for writer.
	// Do NOT defer leave after waiting on done (that deadlocks).
	defer func() {
		h.leave(key, clientID)
		<-done
	}()

	welcome, _ := json.Marshal(map[string]any{
		"type": "connected",
		"room": roomName,
		"from": clientID,
		"data": map[string]string{"client_id": clientID},
	})
	select {
	case cl.send <- welcome:
	default:
	}

	for {
		_ = c.SetReadDeadline(time.Now().Add(60 * time.Minute))
		_, raw, err := c.ReadMessage()
		if err != nil {
			break
		}
		if len(raw) > 64*1024 {
			continue
		}

		var incoming struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(raw, &incoming); err != nil {
			continue
		}
		if incoming.Type == "" {
			incoming.Type = "msg"
		}

		out, err := json.Marshal(map[string]any{
			"type": incoming.Type,
			"room": roomName,
			"from": clientID,
			"data": json.RawMessage(incoming.Data),
		})
		if err != nil {
			continue
		}
		h.broadcast(key, clientID, out)
	}
}
