package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:    4096,
	WriteBufferSize:   8192,
	EnableCompression: true,
	// Accept all origins during development.
	CheckOrigin: func(r *http.Request) bool { return true },
}

var GlobalUDPPort int = 9124

func main() {
	env := flag.String("env", "prod", "Environment: dev or prod")
	flag.Parse()

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

	port := ":9123"
	wsURL := "wss://pacman.yulian-server.duckdns.org/ws?room=<roomID>"
	healthURL := "http://pacman.yulian-server.duckdns.org/health"
	roomsURL := "http://pacman.yulian-server.duckdns.org/rooms"

	if *env == "dev" {
		port = ":8080"
		GlobalUDPPort = 8081
		wsURL = "ws://localhost:8080/ws?room=<roomID>"
		healthURL = "http://localhost:8080/health"
		roomsURL = "http://localhost:8080/rooms"
	}

	log.Printf("🎮 MultiPacMan server starting in %s mode on %s", *env, port)
	log.Printf("   WebSocket : %s", wsURL)
	log.Printf("   Health    : %s", healthURL)
	log.Printf("   Rooms     : %s", roomsURL)

	go startUDPServer(hub, GlobalUDPPort)

	log.Fatal(http.ListenAndServe(port, nil))
}

// Hub manages all active game rooms.
type Hub struct {
	mu         sync.RWMutex
	rooms      map[string]*Game
	Register   chan *Client
	Unregister chan *Client
	UDPConn    *net.UDPConn
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

func startUDPServer(hub *Hub, udpPort int) {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", udpPort))
	if err != nil {
		log.Fatalf("UDP ResolveAddr error: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalf("UDP Listen error: %v", err)
	}
	defer conn.Close()
	log.Printf("   UDP Port  : %d", udpPort)

	hub.UDPConn = conn

	buf := make([]byte, 2048)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("UDP Read error: %v", err)
			continue
		}

		var payload UDPInput
		if err := json.Unmarshal(buf[:n], &payload); err != nil {
			log.Printf("UDP Unmarshal error: %v, raw data: %s", err, string(buf[:n]))
			continue
		}



		hub.mu.Lock()
		clientFound := false
		for _, room := range hub.rooms {
			room.mu.Lock()
			if c, ok := room.clients[payload.ClientID]; ok {
				c.UDPAddr = remoteAddr
				clientFound = true

				if room.state == StatePlaying {
					existing, ok := room.inputQueue[payload.ClientID]
					if !ok || payload.Seq > existing.Seq {
						room.inputQueue[payload.ClientID] = IncomingMsg{
							Type: MsgTypeInput,
							Seq:  payload.Seq,
							DirX: payload.DirX,
							DirY: payload.DirY,
							Dash: payload.Dash,
						}
					}
				}
			}
			room.mu.Unlock()
			if clientFound {
				break
			}
		}
		hub.mu.Unlock()
	}
}
