package main

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Accept all origins during development.
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	hub := NewHub()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		roomID := r.URL.Query().Get("room")
		if roomID == "" {
			roomID = "default"
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("WebSocket upgrade error: %v", err)
			return
		}
		c := NewClient(hub, conn, roomID)
		hub.Register <- c
		go c.WritePump()
		go c.ReadPump()
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	http.HandleFunc("/rooms", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rooms := hub.GetRoomsSummary()
		json.NewEncoder(w).Encode(rooms)
	})

	log.Println("🎮 MultiPacMan server starting on :8080")
	log.Println("   WebSocket : ws://localhost:8080/ws?room=<roomID>")
	log.Println("   Health    : http://localhost:8080/health")
	log.Println("   Rooms     : http://localhost:8080/rooms")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// Hub manages all active game rooms.
type Hub struct {
	mu         sync.RWMutex
	rooms      map[string]*Game
	Register   chan *Client
	Unregister chan *Client
}

func NewHub() *Hub {
	h := &Hub{
		rooms:      make(map[string]*Game),
		Register:   make(chan *Client, 32),
		Unregister: make(chan *Client, 32),
	}
	go h.Run()
	return h
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.Register:
			h.handleRegister(c)
		case c := <-h.Unregister:
			h.handleUnregister(c)
		}
	}
}

func (h *Hub) handleRegister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.rooms[c.RoomID]
	if !ok {
		room = NewGame(c.RoomID)
		h.rooms[c.RoomID] = room
		go room.Run()
		log.Printf("[hub] Created room %q", c.RoomID)
	}

	if !room.AddClient(c) {
		data, _ := json.Marshal(ErrorPayload{
			Type:    MsgTypeError,
			Message: "Room is full or game already finished",
		})
		select {
		case c.Send <- data:
		default:
		}
		close(c.Send)
		return
	}
	log.Printf("[hub] Player %s joined room %q (total: %d)", c.ID, c.RoomID, room.PlayerCount())
}

func (h *Hub) handleUnregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	room, ok := h.rooms[c.RoomID]
	if !ok {
		return
	}
	room.RemoveClient(c)
	log.Printf("[hub] Player %s left room %q (remaining: %d)", c.ID, c.RoomID, room.PlayerCount())

	if room.IsEmpty() {
		delete(h.rooms, c.RoomID)
		log.Printf("[hub] Removed empty room %q", c.RoomID)
	}
}

func (h *Hub) GetRoomsSummary() []RoomSummaryPayload {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]RoomSummaryPayload, 0, len(h.rooms))
	for id, room := range h.rooms {
		out = append(out, RoomSummaryPayload{
			ID:         id,
			State:      room.GetState(),
			Players:    room.PlayerCount(),
			MaxPlayers: MaxPlayers,
		})
	}
	return out
}
