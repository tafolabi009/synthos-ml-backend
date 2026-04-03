package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

// MessageType defines the type of WebSocket message
type MessageType string

const (
	TypeProgress    MessageType = "progress"
	TypeResult      MessageType = "result"
	TypeError       MessageType = "error"
	TypeHeartbeat   MessageType = "heartbeat"
	TypeSubscribe   MessageType = "subscribe"
	TypeUnsubscribe MessageType = "unsubscribe"
)

// Message represents a WebSocket message
type Message struct {
	Type      MessageType    `json:"type"`
	JobID     string         `json:"job_id,omitempty"`
	UserID    string         `json:"user_id,omitempty"`
	Data      interface{}    `json:"data,omitempty"`
	Error     string         `json:"error,omitempty"`
	Timestamp time.Time      `json:"timestamp"`
}

// Client represents a WebSocket client connection
type Client struct {
	ID         string
	UserID     string
	Conn       *websocket.Conn
	Send       chan []byte
	Hub        *Hub
	Jobs       map[string]bool // Subscribed job IDs
	mu         sync.RWMutex
}

// Hub manages WebSocket connections and message broadcasting
type Hub struct {
	clients    map[string]*Client        // Client ID -> Client
	userIndex  map[string]map[string]*Client // User ID -> Client ID -> Client
	jobIndex   map[string]map[string]*Client // Job ID -> Client ID -> Client
	register   chan *Client
	unregister chan *Client
	broadcast  chan *Message
	mu         sync.RWMutex
}

// NewHub creates a new Hub instance
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[string]*Client),
		userIndex:  make(map[string]map[string]*Client),
		jobIndex:   make(map[string]map[string]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan *Message, 256),
	}
}

// Run starts the hub's main loop
func (h *Hub) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case client := <-h.register:
			h.registerClient(client)
		case client := <-h.unregister:
			h.unregisterClient(client)
		case message := <-h.broadcast:
			h.broadcastMessage(message)
		case <-ticker.C:
			h.sendHeartbeats()
		}
	}
}

func (h *Hub) registerClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[client.ID] = client

	// Add to user index
	if client.UserID != "" {
		if _, ok := h.userIndex[client.UserID]; !ok {
			h.userIndex[client.UserID] = make(map[string]*Client)
		}
		h.userIndex[client.UserID][client.ID] = client
	}
}

func (h *Hub) unregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[client.ID]; ok {
		delete(h.clients, client.ID)

		// Remove from user index
		if client.UserID != "" {
			if userClients, ok := h.userIndex[client.UserID]; ok {
				delete(userClients, client.ID)
				if len(userClients) == 0 {
					delete(h.userIndex, client.UserID)
				}
			}
		}

		// Remove from job subscriptions
		client.mu.RLock()
		for jobID := range client.Jobs {
			if jobClients, ok := h.jobIndex[jobID]; ok {
				delete(jobClients, client.ID)
				if len(jobClients) == 0 {
					delete(h.jobIndex, jobID)
				}
			}
		}
		client.mu.RUnlock()

		close(client.Send)
	}
}

func (h *Hub) broadcastMessage(msg *Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	// Send to specific job subscribers
	if msg.JobID != "" {
		if clients, ok := h.jobIndex[msg.JobID]; ok {
			for _, client := range clients {
				select {
				case client.Send <- data:
				default:
					// Channel full, skip
				}
			}
			return
		}
	}

	// Send to specific user
	if msg.UserID != "" {
		if clients, ok := h.userIndex[msg.UserID]; ok {
			for _, client := range clients {
				select {
				case client.Send <- data:
				default:
				}
			}
		}
	}
}

func (h *Hub) sendHeartbeats() {
	msg := &Message{
		Type:      TypeHeartbeat,
		Timestamp: time.Now(),
	}
	data, _ := json.Marshal(msg)

	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, client := range h.clients {
		select {
		case client.Send <- data:
		default:
		}
	}
}

// Register adds a client to the hub
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister removes a client from the hub
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// SubscribeJob subscribes a client to job updates
func (h *Hub) SubscribeJob(clientID, jobID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	client, ok := h.clients[clientID]
	if !ok {
		return
	}

	client.mu.Lock()
	client.Jobs[jobID] = true
	client.mu.Unlock()

	if _, ok := h.jobIndex[jobID]; !ok {
		h.jobIndex[jobID] = make(map[string]*Client)
	}
	h.jobIndex[jobID][clientID] = client
}

// UnsubscribeJob unsubscribes a client from job updates
func (h *Hub) UnsubscribeJob(clientID, jobID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	client, ok := h.clients[clientID]
	if !ok {
		return
	}

	client.mu.Lock()
	delete(client.Jobs, jobID)
	client.mu.Unlock()

	if clients, ok := h.jobIndex[jobID]; ok {
		delete(clients, clientID)
		if len(clients) == 0 {
			delete(h.jobIndex, jobID)
		}
	}
}

// SendProgress sends a progress update for a job
func (h *Hub) SendProgress(jobID string, progress interface{}) {
	h.broadcast <- &Message{
		Type:      TypeProgress,
		JobID:     jobID,
		Data:      progress,
		Timestamp: time.Now(),
	}
}

// SendResult sends a result for a job
func (h *Hub) SendResult(jobID string, result interface{}) {
	h.broadcast <- &Message{
		Type:      TypeResult,
		JobID:     jobID,
		Data:      result,
		Timestamp: time.Now(),
	}
}

// SendError sends an error for a job
func (h *Hub) SendError(jobID string, err error) {
	h.broadcast <- &Message{
		Type:      TypeError,
		JobID:     jobID,
		Error:     err.Error(),
		Timestamp: time.Now(),
	}
}

// SendToUser sends a message to all connections for a user
func (h *Hub) SendToUser(userID string, msgType MessageType, data interface{}) {
	h.broadcast <- &Message{
		Type:      msgType,
		UserID:    userID,
		Data:      data,
		Timestamp: time.Now(),
	}
}

// NewClient creates a new client
func NewClient(conn *websocket.Conn, userID string, hub *Hub) *Client {
	return &Client{
		ID:     uuid.New().String(),
		UserID: userID,
		Conn:   conn,
		Send:   make(chan []byte, 256),
		Hub:    hub,
		Jobs:   make(map[string]bool),
	}
}

// ReadPump pumps messages from the WebSocket connection to the hub
func (c *Client) ReadPump() {
	defer func() {
		c.Hub.Unregister(c)
		c.Conn.Close()
	}()

	for {
		_, msgData, err := c.Conn.ReadMessage()
		if err != nil {
			return
		}

		var msg Message
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case TypeSubscribe:
			if msg.JobID != "" {
				c.Hub.SubscribeJob(c.ID, msg.JobID)
			}
		case TypeUnsubscribe:
			if msg.JobID != "" {
				c.Hub.UnsubscribeJob(c.ID, msg.JobID)
			}
		}
	}
}

// WritePump pumps messages from the hub to the WebSocket connection
func (c *Client) WritePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// GetClientCount returns the number of connected clients
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// GetJobSubscriberCount returns the number of subscribers for a job
func (h *Hub) GetJobSubscriberCount(jobID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if clients, ok := h.jobIndex[jobID]; ok {
		return len(clients)
	}
	return 0
}

// SendRaw sends raw bytes to a specific client
func (h *Hub) SendRaw(clientID string, data []byte) error {
	h.mu.RLock()
	client, ok := h.clients[clientID]
	h.mu.RUnlock()

	if !ok {
		return fmt.Errorf("client not found: %s", clientID)
	}

	select {
	case client.Send <- data:
		return nil
	default:
		return fmt.Errorf("client channel full")
	}
}
