package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			ctxList, cancelList := context.WithTimeout(context.Background(), 5*time.Second)
			channels, err := dbListChannels(ctxList)
			cancelList()
			if err != nil {
				continue
			}

			for _, ch := range channels {
				slug := ch.Slug
				ctxGet, cancelGet := context.WithTimeout(context.Background(), 5*time.Second)
				list, err := dbGetScheduledMessages(ctxGet, slug)
				cancelGet()
				if err != nil {
					continue
				}

				now := time.Now()
				newList := make([]Message, 0)
				for _, msg := range *list {
					if msg.Timestamp.Before(now) {
						go func(m *Message, s string) {
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()

							m.ID = getMessageNextId(ctx, s)
							m.Timestamp = time.Now()
							m.Author = "Scheduled"
							m.AuthorId = "0"
							setMessage(ctx, s, m, false)
							go SendWebhook(context.Background(), s, "create", m)
							go pushFcmMessage(s, m)
						}(&msg, slug)
					} else {
						newList = append(newList, msg)
					}
				}

				ctxSave, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
				dbSaveScheduledMessages(ctxSave, slug, &newList)
				cancelSave()
			}
		}
	}()
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
