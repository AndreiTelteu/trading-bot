package handlers

import (
	ws "trading-go/internal/websocket"

	"github.com/gofiber/contrib/websocket"
)

var wsHub *ws.Hub

func InitWebSocket(hub *ws.Hub) {
	wsHub = hub
	go wsHub.Run()
}

func GetWSHub() *ws.Hub {
	return wsHub
}

func HandleWebSocketConn(c *websocket.Conn, hub *ws.Hub) {
	client := ws.NewClient(hub, c)
	hub.Register <- client

	go client.WritePump()
	go client.ReadPump()
}
