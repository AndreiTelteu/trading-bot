package handlers

import (
	"log"
	"time"

	"trading-go/internal/backtest"
	"trading-go/internal/database"
	ws "trading-go/internal/websocket"

	"github.com/gofiber/contrib/websocket"
)

var wsHub *ws.Hub

func InitWebSocket(hub *ws.Hub) {
	wsHub = hub
	go wsHub.Run()

	// Initialize the broadcaster for use throughout the application
	ws.InitBroadcaster(hub)
}

func GetWSHub() *ws.Hub {
	return wsHub
}

func HandleWebSocketConn(c *websocket.Conn, hub *ws.Hub) {
	client := ws.NewClient(hub, c)
	hub.Register <- client

	// Keep write pump async, but keep read pump blocking in this handler.
	// If both are started as goroutines, the handler can return immediately,
	// which closes the connection and causes rapid reconnect loops.
	go client.WritePump()

	// Send connection established message
	ws.SendToClient(client, "connection_established", map[string]interface{}{
		"server_time": time.Now().UTC().Format(time.RFC3339),
	})

	// Send initial data sync
	sendFullSyncToClient(client)

	// Block on read pump for lifetime of the connection
	client.ReadPump()
}

// sendFullSyncToClient sends all current data to a newly connected client
func sendFullSyncToClient(client *ws.Client) {
	// Send wallet data
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err == nil {
		var totalValue float64
		totalValue = wallet.Balance

		// Calculate total value including positions
		var positions []database.Position
		database.DB.Where("status = ?", "open").Find(&positions)
		for _, pos := range positions {
			if pos.CurrentPrice != nil {
				totalValue += pos.Amount * (*pos.CurrentPrice)
			}
		}

		ws.SendToClient(client, "wallet_update", map[string]interface{}{
			"balance":     wallet.Balance,
			"currency":    wallet.Currency,
			"total_value": totalValue,
		})
	}

	// Send positions
	if allPositions, err := database.ListPositionsForDisplay(); err == nil {
		ws.SendToClient(client, "positions_update", allPositions)
	}

	// Send recent activity logs (limit to 10 to avoid large messages)
	var logs []database.ActivityLog
	if err := database.DB.Order("timestamp DESC").Limit(10).Find(&logs).Error; err == nil {
		ws.SendToClient(client, "activity_log_bulk", logs)
	}

	// Send only the most recent snapshot
	var snapshot database.PortfolioSnapshot
	if err := database.DB.Order("timestamp DESC").First(&snapshot).Error; err == nil {
		ws.SendToClient(client, "snapshot_update", map[string]interface{}{
			"timestamp":   snapshot.Timestamp,
			"total_value": snapshot.TotalValue,
		})
	}

	// Send recent orders
	var orders []database.Order
	if err := database.DB.Order("executed_at DESC").Limit(10).Find(&orders).Error; err == nil {
		ws.SendToClient(client, "orders_update", orders)
	}

	var job database.BacktestJob
	if err := database.DB.Order("created_at DESC").First(&job).Error; err == nil {
		response, buildErr := backtest.BuildBacktestJobResponse(&job)
		if buildErr == nil {
			ws.SendToClient(client, "backtest_status", response)
		}
	}

	log.Printf("[WS] Full sync sent to client")
}

// BroadcastWalletUpdate broadcasts wallet update to all clients
func BroadcastWalletUpdate(balance float64, currency string) {
	var totalValue float64 = balance

	var positions []database.Position
	database.DB.Where("status = ?", "open").Find(&positions)
	for _, pos := range positions {
		if pos.CurrentPrice != nil {
			totalValue += pos.Amount * (*pos.CurrentPrice)
		}
	}

	ws.BroadcastWalletUpdate(balance, currency, totalValue)
}

// BroadcastPositionsUpdate broadcasts all positions to all clients
func BroadcastPositionsUpdate() {
	if positions, err := database.ListPositionsForDisplay(); err == nil {
		ws.BroadcastPositionsUpdate(positions)
	}
}

// BroadcastActivityLogNew broadcasts a new activity log entry
func BroadcastActivityLogNew(log *database.ActivityLog) {
	ws.BroadcastActivityLogNew(log)
}

// BroadcastSnapshotUpdate broadcasts a new portfolio snapshot
func BroadcastSnapshotUpdate(totalValue float64) {
	ws.BroadcastSnapshotUpdate(time.Now().UTC().Format(time.RFC3339), totalValue)
}
