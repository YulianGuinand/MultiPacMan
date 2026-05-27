package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
	maxMsgSize = 8192
)

// generateID creates a short random hex ID.
func generateID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", b)
}

// Client wraps a WebSocket connection for a single player.
// It owns two goroutines: ReadPump and WritePump.
type Client struct {
	ID     string
	RoomID string
	hub    *Hub
	conn   *websocket.Conn
	// Send is a buffered channel of outgoing JSON messages.
	// WritePump drains it; capacity 128 covers one full game-state broadcast burst.
	Send chan []byte
}

func NewClient(hub *Hub, conn *websocket.Conn, roomID string) *Client {
	return &Client{
		ID:     generateID(),
		RoomID: roomID,
		hub:    hub,
		conn:   conn,
		Send:   make(chan []byte, 128),
	}
}

// ReadPump pumps messages from the WebSocket to the game room.
// One goroutine per client.
func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMsgSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				log.Printf("[client %s] read error: %v", c.ID, err)
			}
			break
		}

		// Route to the correct game room (read-lock only).
		c.hub.mu.RLock()
		room, ok := c.hub.rooms[c.RoomID]
		c.hub.mu.RUnlock()

		if ok {
			room.HandleMessage(c.ID, data)
		}
	}
}

// WritePump pumps messages from Send channel to the WebSocket.
// One goroutine per client. Also sends WebSocket pings.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.Send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Channel was closed by Hub.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// SendJSON marshals v and pushes it to the Send channel (non-blocking).
func (c *Client) SendJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[client %s] SendJSON marshal error: %v", c.ID, err)
		return
	}
	select {
	case c.Send <- data:
	default:
		log.Printf("[client %s] Send buffer full, dropping message", c.ID)
	}
}
