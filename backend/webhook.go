package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type WebhookPayload struct {
	Action      string    `json:"action"`
	Message     Message   `json:"message"`
	Timestamp   time.Time `json:"timestamp"`
	VerifyToken string    `json:"verifyToken"`
}

func SendWebhook(ctx context.Context, slug string, action string, message *Message) {
	// Load per-channel config for webhook settings
	chCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cfg := getChannelConfig(chCtx, slug)
	if cfg.WebhookURL == "" {
		return
	}

	payload := WebhookPayload{
		Action:      action,
		Message:     *message,
		Timestamp:   time.Now(),
		VerifyToken: cfg.VerifyToken,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error converting webhook data to JSON: %v\n", err)
		return
	}

	httpCtx, httpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer httpCancel()

	req, err := http.NewRequestWithContext(httpCtx, "POST", cfg.WebhookURL, bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error creating webhook request: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "TheChannel-Webhook")

	// Warning! Default is not secure
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending webhook: %v\n", err)
		return
	}
	defer resp.Body.Close()

	log.Printf("Sent webhook for action '%s' on message %d. Response code: %d\n",
		action, message.ID, resp.StatusCode)
}
