package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/tafolabi009/backend/go_backend/pkg/database"
)

// Event represents a webhook event payload
type Event struct {
	Type      string      `json:"type"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// Dispatch fires webhook events to all active webhooks for a user that subscribe to the given event type.
// It runs asynchronously and does not block the caller.
func Dispatch(eventType string, userID string, data interface{}) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		db := database.GetDB()
		if db == nil {
			return
		}

		// Find active webhooks for this user that subscribe to this event
		rows, err := db.Query(ctx,
			`SELECT id, url, secret FROM webhooks
			 WHERE user_id = $1 AND is_active = true AND $2 = ANY(events)`,
			userID, eventType)
		if err != nil {
			return
		}
		defer rows.Close()

		event := Event{
			Type:      eventType,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Data:      data,
		}

		payload, _ := json.Marshal(event)

		for rows.Next() {
			var webhookID, url, secret string
			if err := rows.Scan(&webhookID, &url, &secret); err != nil {
				continue
			}
			go deliverWebhook(webhookID, url, secret, payload, eventType)
		}
	}()
}

func deliverWebhook(webhookID, url, secret string, payload []byte, eventType string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Sign payload with HMAC-SHA256
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	signature := hex.EncodeToString(mac.Sum(nil))

	// Send HTTP request
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		logDeliveryFailure(webhookID, eventType, payload, err.Error(), 0)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Synthos-Signature", signature)
	req.Header.Set("X-Synthos-Event", eventType)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	duration := int(time.Since(start).Milliseconds())

	// Log delivery result
	db := database.GetDB()
	if db == nil {
		return
	}
	deliveryID := "whd_" + uuid.New().String()[:8]

	var respStatus int
	var respBody string
	var success bool
	if err != nil {
		respBody = err.Error()
	} else {
		respStatus = resp.StatusCode
		success = resp.StatusCode >= 200 && resp.StatusCode < 300
		defer resp.Body.Close()
		// Read first 500 bytes of response
		buf := make([]byte, 500)
		n, _ := resp.Body.Read(buf)
		respBody = string(buf[:n])
	}

	db.Exec(context.Background(),
		`INSERT INTO webhook_deliveries (id, webhook_id, event_type, payload, response_status, response_body, success, duration_ms)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		deliveryID, webhookID, eventType, payload, respStatus, respBody, success, duration)

	// Update webhook last_triggered_at and failure_count
	if success {
		db.Exec(context.Background(),
			`UPDATE webhooks SET last_triggered_at = NOW(), failure_count = 0, updated_at = NOW() WHERE id = $1`, webhookID)
	} else {
		db.Exec(context.Background(),
			`UPDATE webhooks SET failure_count = failure_count + 1, updated_at = NOW() WHERE id = $1`, webhookID)
	}
}

func logDeliveryFailure(webhookID, eventType string, payload []byte, errMsg string, duration int) {
	db := database.GetDB()
	if db == nil {
		return
	}
	deliveryID := "whd_" + uuid.New().String()[:8]
	db.Exec(context.Background(),
		`INSERT INTO webhook_deliveries (id, webhook_id, event_type, payload, response_status, response_body, success, duration_ms)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		deliveryID, webhookID, eventType, payload, 0, errMsg, false, duration)

	db.Exec(context.Background(),
		`UPDATE webhooks SET failure_count = failure_count + 1, updated_at = NOW() WHERE id = $1`, webhookID)
}
