package websocket

import (
	"encoding/json"
	"log"
	"time"

	"github.com/gofiber/contrib/websocket"
)

type Client struct {
	Hub  *Hub
	Conn *websocket.Conn
	Send chan *Message
}

func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		Hub:  hub,
		Conn: conn,
		Send: make(chan *Message, 256),
	}
}

func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(512)
	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			break
		}

		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Error unmarshaling message: %v", err)
			continue
		}

		c.handleMessage(&msg)
	}
}

func (c *Client) WritePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			data, err := json.Marshal(message)
			if err != nil {
				log.Printf("Error marshaling message: %v", err)
				continue
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) handleMessage(msg *Message) {
	switch msg.Type {
	case "ping":
		// Extend read deadline (keep connection alive)
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		// Respond with pong
		c.Send <- &Message{
			Type:    "pong",
			Payload: map[string]interface{}{"time": time.Now().Unix()},
		}
	case "request_full_sync":
		// Client is requesting full data sync (on connect or reconnect)
		c.sendFullSync()
	case "join_room":
		if roomName, ok := msg.Payload.(string); ok {
			c.Hub.JoinRoom(c, roomName)
			c.Send <- &Message{
				Type:    "room_joined",
				Payload: roomName,
			}
		}
	case "leave_room":
		if roomName, ok := msg.Payload.(string); ok {
			c.Hub.LeaveRoom(c, roomName)
			c.Send <- &Message{
				Type:    "room_left",
				Payload: roomName,
			}
		}
	case "subscribe":
		if payload, ok := msg.Payload.(map[string]interface{}); ok {
			if symbol, ok := payload["symbol"].(string); ok {
				roomName := "ticker:" + symbol
				c.Hub.JoinRoom(c, roomName)
				c.Send <- &Message{
					Type:    "subscribed",
					Payload: symbol,
				}
			}
		}
	case "unsubscribe":
		if payload, ok := msg.Payload.(map[string]interface{}); ok {
			if symbol, ok := payload["symbol"].(string); ok {
				roomName := "ticker:" + symbol
				c.Hub.LeaveRoom(c, roomName)
				c.Send <- &Message{
					Type:    "unsubscribed",
					Payload: symbol,
				}
			}
		}
	default:
		c.Hub.Broadcast <- msg
	}
}

// sendFullSync sends all current data to the client (for initial load or reconnect)
func (c *Client) sendFullSync() {
	// This will be implemented to fetch and send current state
	// The actual implementation will be in the handlers package to avoid import cycles
}
