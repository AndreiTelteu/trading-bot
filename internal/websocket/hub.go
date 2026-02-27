package websocket

import (
	"encoding/json"
	"sync"
)

type Message struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

type Hub struct {
	Clients    map[*Client]bool
	Rooms      map[string]map[*Client]bool
	Register   chan *Client
	Unregister chan *Client
	Broadcast  chan *Message
	Mu         sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		Clients:    make(map[*Client]bool),
		Rooms:      make(map[string]map[*Client]bool),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Broadcast:  make(chan *Message),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.Mu.Lock()
			h.Clients[client] = true
			h.Mu.Unlock()

		case client := <-h.Unregister:
			h.Mu.Lock()
			if _, ok := h.Clients[client]; ok {
				delete(h.Clients, client)
				close(client.Send)
				for roomName, room := range h.Rooms {
					if _, ok := room[client]; ok {
						delete(room, client)
						if len(room) == 0 {
							delete(h.Rooms, roomName)
						}
					}
				}
			}
			h.Mu.Unlock()

		case message := <-h.Broadcast:
			h.Mu.RLock()
			for client := range h.Clients {
				select {
				case client.Send <- message:
				default:
					close(client.Send)
					delete(h.Clients, client)
				}
			}
			h.Mu.RUnlock()
		}
	}
}

func (h *Hub) BroadcastRoom(roomName string, msg *Message) {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	if room, ok := h.Rooms[roomName]; ok {
		for client := range room {
			select {
			case client.Send <- msg:
			default:
				close(client.Send)
				delete(h.Clients, client)
			}
		}
	}
}

func (h *Hub) BroadcastMsg(msg *Message) {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	for client := range h.Clients {
		select {
		case client.Send <- msg:
		default:
			close(client.Send)
			delete(h.Clients, client)
		}
	}
}

func (h *Hub) JoinRoom(client *Client, roomName string) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	if h.Rooms[roomName] == nil {
		h.Rooms[roomName] = make(map[*Client]bool)
	}
	h.Rooms[roomName][client] = true
}

func (h *Hub) LeaveRoom(client *Client, roomName string) {
	h.Mu.Lock()
	defer h.Mu.Unlock()

	if room, ok := h.Rooms[roomName]; ok {
		delete(room, client)
		if len(room) == 0 {
			delete(h.Rooms, roomName)
		}
	}
}

func (h *Hub) GetRoomClientCount(roomName string) int {
	h.Mu.RLock()
	defer h.Mu.RUnlock()

	if room, ok := h.Rooms[roomName]; ok {
		return len(room)
	}
	return 0
}

func MarshalMessage(msg *Message) []byte {
	data, _ := json.Marshal(msg)
	return data
}
