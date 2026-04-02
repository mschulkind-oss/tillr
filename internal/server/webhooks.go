package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/internal/db"
	"github.com/mschulkind-oss/tillr/internal/models"
)

const (
	webhookTimeout    = 10 * time.Second
	webhookMaxRetries = 3
)

// GenerateWebhookID creates a short random hex ID for webhooks.
func GenerateWebhookID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID on rand failure.
		return fmt.Sprintf("wh-%d", time.Now().UnixNano()%1_000_000)
	}
	return "wh-" + hex.EncodeToString(b)
}

// DispatchWebhooks sends an event to all matching active webhooks asynchronously.
// Called from InsertEvent's deferred goroutine — already running async, so no
// need to spawn another goroutine for the DB query.
func DispatchWebhooks(database *sql.DB, event *models.Event) {
	webhooks, err := db.ListActiveWebhooks(database)
	if err != nil {
		log.Printf("webhook: failed to list webhooks: %v", err)
		return
	}

	for _, wh := range webhooks {
		if !matchesEvent(wh.Events, event.EventType) {
			continue
		}
		go deliverWebhook(wh, event)
	}
}

// matchesEvent checks if a webhook's event filter matches the given event type.
// An empty list or "[]" means subscribe to all events.
func matchesEvent(eventsJSON, eventType string) bool {
	eventsJSON = strings.TrimSpace(eventsJSON)
	if eventsJSON == "" || eventsJSON == "[]" {
		return true
	}

	var events []string
	if err := json.Unmarshal([]byte(eventsJSON), &events); err != nil {
		return true // on parse error, deliver to be safe
	}

	if len(events) == 0 {
		return true
	}

	for _, e := range events {
		if e == eventType {
			return true
		}
	}
	return false
}

// deliverWebhook sends the event payload to a single webhook with retry logic.
func deliverWebhook(wh models.Webhook, event *models.Event) {
	delivery := models.WebhookDelivery{
		ID:        fmt.Sprintf("del-%d", time.Now().UnixNano()),
		Event:     event.EventType,
		Timestamp: event.CreatedAt,
		Data: map[string]any{
			"event_id":   event.ID,
			"project_id": event.ProjectID,
			"feature_id": event.FeatureID,
			"event_type": event.EventType,
			"data":       event.Data,
			"created_at": event.CreatedAt,
		},
	}

	body, err := json.Marshal(delivery)
	if err != nil {
		log.Printf("webhook %s: failed to marshal payload: %v", wh.ID, err)
		return
	}

	client := &http.Client{Timeout: webhookTimeout}

	for attempt := 1; attempt <= webhookMaxRetries; attempt++ {
		req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(body))
		if err != nil {
			log.Printf("webhook %s: failed to create request: %v", wh.ID, err)
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Tillr-Webhook/1.0")
		req.Header.Set("X-Tillr-Event", event.EventType)
		req.Header.Set("X-Tillr-Delivery", delivery.ID)

		if wh.Secret != "" {
			sig := computeHMAC(body, wh.Secret)
			req.Header.Set("X-Tillr-Signature", "sha256="+sig)
		}

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("webhook %s: attempt %d/%d failed: %v", wh.ID, attempt, webhookMaxRetries, err)
			if attempt < webhookMaxRetries {
				backoff := time.Duration(1<<uint(attempt-1)) * time.Second
				time.Sleep(backoff)
			}
			continue
		}
		resp.Body.Close() //nolint:errcheck

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return // success
		}

		log.Printf("webhook %s: attempt %d/%d got status %d", wh.ID, attempt, webhookMaxRetries, resp.StatusCode)
		if attempt < webhookMaxRetries {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			time.Sleep(backoff)
		}
	}

	log.Printf("webhook %s: all %d attempts exhausted for event %s", wh.ID, webhookMaxRetries, event.EventType)
}

// computeHMAC returns the hex-encoded HMAC-SHA256 of body using the given secret.
func computeHMAC(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// SendTestWebhook sends a test event to a specific webhook and returns the HTTP status.
func SendTestWebhook(wh *models.Webhook) (int, error) {
	delivery := models.WebhookDelivery{
		ID:        fmt.Sprintf("test-%d", time.Now().UnixNano()),
		Event:     "webhook.test",
		Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Data: map[string]any{
			"message": "This is a test webhook delivery from Tillr.",
		},
	}

	body, err := json.Marshal(delivery)
	if err != nil {
		return 0, fmt.Errorf("marshalling test payload: %w", err)
	}

	client := &http.Client{Timeout: webhookTimeout}

	req, err := http.NewRequest(http.MethodPost, wh.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Tillr-Webhook/1.0")
	req.Header.Set("X-Tillr-Event", "webhook.test")
	req.Header.Set("X-Tillr-Delivery", delivery.ID)

	if wh.Secret != "" {
		sig := computeHMAC(body, wh.Secret)
		req.Header.Set("X-Tillr-Signature", "sha256="+sig)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	return resp.StatusCode, nil
}
