package websocket

import (
	"encoding/json"
	"log"
	"time"
)

// Broadcaster is a singleton that allows broadcasting messages from anywhere in the application
type Broadcaster struct {
	hub *Hub
}

var broadcaster *Broadcaster

// InitBroadcaster initializes the broadcaster singleton
func InitBroadcaster(hub *Hub) {
	broadcaster = &Broadcaster{hub: hub}
}

// GetBroadcaster returns the broadcaster instance
func GetBroadcaster() *Broadcaster {
	return broadcaster
}

// Broadcast sends a message to all connected clients
func Broadcast(msgType string, payload interface{}) {
	if broadcaster == nil || broadcaster.hub == nil {
		log.Printf("[WS Broadcast] Warning: Broadcaster not initialized, message dropped: %s", msgType)
		return
	}

	msg := &Message{
		Type:    msgType,
		Payload: payload,
	}

	broadcaster.hub.Broadcast <- msg
}

// BroadcastRoom sends a message to clients in a specific room
func BroadcastRoom(roomName string, msgType string, payload interface{}) {
	if broadcaster == nil || broadcaster.hub == nil {
		log.Printf("[WS Broadcast] Warning: Broadcaster not initialized, message dropped: %s to room %s", msgType, roomName)
		return
	}

	msg := &Message{
		Type:    msgType,
		Payload: payload,
	}

	broadcaster.hub.BroadcastRoom(roomName, msg)
}

// BroadcastJSON marshals the payload and broadcasts it
func BroadcastJSON(msgType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	var rawPayload map[string]interface{}
	if err := json.Unmarshal(data, &rawPayload); err != nil {
		return err
	}

	Broadcast(msgType, rawPayload)
	return nil
}

// Helper methods for specific message types

// BroadcastWalletUpdate broadcasts wallet balance updates
func BroadcastWalletUpdate(balance float64, currency string, totalValue float64) {
	Broadcast("wallet_update", map[string]interface{}{
		"balance":     balance,
		"currency":    currency,
		"total_value": totalValue,
	})
}

// BroadcastPositionsUpdate broadcasts all positions
func BroadcastPositionsUpdate(positions interface{}) {
	Broadcast("positions_update", positions)
}

// BroadcastPositionUpdate broadcasts a single position update
func BroadcastPositionUpdate(position interface{}) {
	Broadcast("position_update", position)
}

// BroadcastPositionClosed broadcasts when a position is closed
func BroadcastPositionClosed(positionID uint, symbol string, reason string, pnl float64) {
	Broadcast("position_closed", map[string]interface{}{
		"position_id": positionID,
		"symbol":      symbol,
		"reason":      reason,
		"pnl":         pnl,
	})
}

// BroadcastOrdersUpdate broadcasts recent orders
func BroadcastOrdersUpdate(orders interface{}) {
	Broadcast("orders_update", orders)
}

// BroadcastTrendingUpdate broadcasts trending coin analysis
func BroadcastTrendingUpdate(coins interface{}) {
	Broadcast("trending_update", coins)
}

// BroadcastSnapshotUpdate broadcasts a new portfolio snapshot
func BroadcastSnapshotUpdate(timestamp interface{}, totalValue float64) {
	Broadcast("snapshot_update", map[string]interface{}{
		"timestamp":   timestamp,
		"total_value": totalValue,
	})
}

// BroadcastActivityLogNew broadcasts a new activity log entry
func BroadcastActivityLogNew(log interface{}) {
	Broadcast("activity_log_new", log)
}

// BroadcastActivityLogBulk broadcasts bulk activity logs (for initial sync)
func BroadcastActivityLogBulk(logs interface{}) {
	Broadcast("activity_log_bulk", logs)
}

// BroadcastAnalysisComplete broadcasts when trending analysis is complete
func BroadcastAnalysisComplete(timestamp string, coinsAnalyzed int, tradesOpened int) {
	Broadcast("analysis_complete", map[string]interface{}{
		"timestamp":      timestamp,
		"coins_analyzed": coinsAnalyzed,
		"trades_opened":  tradesOpened,
	})
}

// BroadcastTradeExecuted broadcasts when a trade is executed
func BroadcastTradeExecuted(tradeType string, symbol string, amount float64, price float64, newBalance float64) {
	Broadcast("trade_executed", map[string]interface{}{
		"type":        tradeType,
		"symbol":      symbol,
		"amount":      amount,
		"price":       price,
		"new_balance": newBalance,
	})
}

// BroadcastConnectionEstablished sends to a specific client on connection
func BroadcastConnectionEstablished(client *Client, clientID string) {
	msg := &Message{
		Type: "connection_established",
		Payload: map[string]interface{}{
			"client_id":   clientID,
			"server_time": time.Now().UTC().Format(time.RFC3339),
		},
	}
	select {
	case client.Send <- msg:
	default:
		log.Printf("[WS Broadcast] Failed to send connection_established to client")
	}
}

// SendToClient sends a message to a specific client
func SendToClient(client *Client, msgType string, payload interface{}) {
	if client == nil {
		return
	}
	msg := &Message{
		Type:    msgType,
		Payload: payload,
	}
	select {
	case client.Send <- msg:
	default:
		log.Printf("[WS Broadcast] Failed to send %s to client (channel full or closed)", msgType)
	}
}
