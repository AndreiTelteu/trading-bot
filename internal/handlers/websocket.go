package handlers

import (
	ws "trading-go/internal/websocket"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
)

var wsHub *ws.Hub

func InitWebSocket(hub *ws.Hub) {
	wsHub = hub
	go wsHub.Run()
}

// HandleWebSocket handles WebSocket connections using fiber/contrib/websocket
func HandleWebSocket(c *fiber.Ctx) error {
	// Use fiber/contrib/websocket middleware style
	return websocket.New(func(c *websocket.Conn) {
		client := ws.NewClient(wsHub, c)
		wsHub.Register <- client

		go client.WritePump()
		go client.ReadPump()
	})(c)
}
