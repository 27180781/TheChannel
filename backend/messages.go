package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

func getMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	ch := channelFromCtx(r)

	offsetFromClient := r.URL.Query().Get("offset")
	limitFromClient := r.URL.Query().Get("limit")
	direction := r.URL.Query().Get("direction")

	offset, err := strconv.Atoi(offsetFromClient)
	if err != nil {
		offset = 0
	}

	limit, err := strconv.Atoi(limitFromClient)
	if err != nil {
		limit = 20
	}

	isAdmin := hasChannelRole(r, slug, RoleWriter)
	countViews := ch != nil && ch.Features.CountViews

	// ETag: skip expensive query if content hasn't changed since client's copy
	etag := `"` + getLastModified(ctx, slug) + `"`
	if etag != `""` {
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	messages, err := funcGetMessageRange(ctx, slug, int64(offset), int64(limit), isAdmin, countViews, direction)
	if err != nil {
		log.Printf("Failed to get messages: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)

	addViewsToMessages(ctx, slug, countViews, messages)
}

func addMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var message Message
	var err error
	defer r.Body.Close()

	session, _ := store.Get(r, cookieName)
	user, _ := session.Values["user"].(Session)

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	message.ID = getMessageNextId(ctx, slug)
	message.Type = body.Type
	message.Author = user.PublicName
	message.AuthorId = user.ID
	message.Timestamp = time.Now()
	message.Text = body.Text
	message.File = body.File
	message.Views = 0
	message.IsAds = body.IsAds

	if err = setMessage(ctx, slug, &message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	go SendWebhook(context.Background(), slug, "create", &message)
	go pushFcmMessage(slug, &message)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}

func updateMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	var err error
	defer r.Body.Close()

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	body.LastEdit = time.Now()

	if err := setMessage(ctx, slug, &body, true); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	go SendWebhook(context.Background(), slug, "update", &body)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

func deleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	id := chi.URLParam(r, "id")

	idInt, _ := strconv.Atoi(id)
	message := Message{ID: idInt, Deleted: true}

	if err := funcDeleteMessage(ctx, slug, id); err != nil {
		response := Response{Success: false}
		json.NewEncoder(w).Encode(response)
		return
	}

	go SendWebhook(context.Background(), slug, "delete", &message)

	response := Response{Success: true}
	json.NewEncoder(w).Encode(response)
}

// getEvents serves a Server-Sent Events stream backed by a Redis Stream.
//
// Using Redis Streams instead of pub/sub enables:
//   - Horizontal scaling: multiple backend instances can all serve SSE
//   - Reconnection: clients send Last-Event-ID to resume without missing events
//   - Durability: stream retains the last ~1000 events (configurable in publishEvent)
func getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	slug := channelSlugFromCtx(r)
	streamKey := fmt.Sprintf("channel:%s:events", slug)

	// Support SSE reconnection: browser sends Last-Event-ID with the stream entry ID
	// from the last event it received. On fresh connect, start from "now".
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		lastID = "$"
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if _, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n"); err != nil {
		return
	}
	flusher.Flush()

	go increaseCounterSSE(slug)
	defer decreaseCounterSSE(slug)

	clientCtx := r.Context()
	lastHeartbeat := time.Now()
	const blockDuration = 5 * time.Second
	const heartbeatInterval = 25 * time.Second

	for {
		if clientCtx.Err() != nil {
			return
		}

		// Send heartbeat if it's been too long
		if time.Since(lastHeartbeat) >= heartbeatInterval {
			if _, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n"); err != nil {
				return
			}
			flusher.Flush()
			lastHeartbeat = time.Now()
		}

		// XREAD blocks until new messages arrive or timeout expires.
		// A short block duration lets us send periodic heartbeats and check for disconnect.
		streams, err := rdb.XRead(clientCtx, &redis.XReadArgs{
			Streams: []string{streamKey, lastID},
			Count:   50,
			Block:   blockDuration,
		}).Result()

		if clientCtx.Err() != nil {
			return
		}
		if err != nil {
			// redis.Nil = block timeout with no messages; other errors: brief pause then retry
			if err != redis.Nil {
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				data, _ := msg.Values["data"].(string)
				if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", lastID, data); err != nil {
					return
				}
			}
		}
		flusher.Flush()
	}
}
