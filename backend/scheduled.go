package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			runScheduledMessages()
		}
	}()
}

// runScheduledMessages only processes channels that have at least one message
// due before now, using the "scheduled:due_channels" sorted set (score = earliest
// due timestamp). This avoids querying every channel every minute.
func runScheduledMessages() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := float64(time.Now().Unix())

	// Get all channels with at least one message due by now
	slugs, err := rdb.ZRangeByScore(ctx, "scheduled:due_channels", &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", now),
	}).Result()
	if err != nil || len(slugs) == 0 {
		return
	}

	for _, slug := range slugs {
		slug := slug
		ctxGet, cancelGet := context.WithTimeout(context.Background(), 5*time.Second)
		list, err := dbGetScheduledMessages(ctxGet, slug)
		cancelGet()
		if err != nil {
			continue
		}

		nowTime := time.Now()
		newList := make([]Message, 0)
		for _, msg := range *list {
			if msg.Timestamp.Before(nowTime) {
				go func(m *Message, s string) {
					postCtx, postCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer postCancel()
					m.ID = getMessageNextId(postCtx, s)
					m.Timestamp = time.Now()
					m.Author = "Scheduled"
					m.AuthorId = "0"
					setMessage(postCtx, s, m, false)
					go SendWebhook(context.Background(), s, "create", m)
					go pushFcmMessage(s, m)
				}(&msg, slug)
			} else {
				newList = append(newList, msg)
			}
		}

		ctxSave, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
		dbSaveScheduledMessages(ctxSave, slug, &newList) // also updates the sorted set
		cancelSave()
	}
}

func getScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	messages, err := dbGetScheduledMessages(ctx, slug)
	if err != nil {
		http.Error(w, "error getting messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func updateScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	defer r.Body.Close()

	slug := channelSlugFromCtx(r)

	var messages []Message
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		http.Error(w, "error decoding messages", http.StatusBadRequest)
		return
	}

	if err := dbSaveScheduledMessages(ctx, slug, &messages); err != nil {
		http.Error(w, "error saving messages", http.StatusInternalServerError)
		return
	}

	var res = Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
