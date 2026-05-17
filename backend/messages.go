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

func getEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	slug := channelSlugFromCtx(r)

	clientCtx := r.Context()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n")
	if err != nil {
		return
	}
	flusher.Flush()

	go increaseCounterSSE(slug)
	defer decreaseCounterSSE(slug)

	eventChannel := fmt.Sprintf("events:%s", slug)
	pubsub := rdb.Subscribe(r.Context(), eventChannel)
	defer pubsub.Close()

	if _, err := pubsub.Receive(clientCtx); err != nil {
		http.Error(w, "Failed to subscribe to events", http.StatusInternalServerError)
		return
	}

	ch := pubsub.Channel()
	for {
		select {
		case <-clientCtx.Done():
			return

		case <-heartbeat.C:
			_, err := fmt.Fprintf(w, "data: {\"type\": \"heartbeat\"}\n\n")
			if err != nil {
				return
			}
			flusher.Flush()

		case msg, ok := <-ch:
			if !ok {
				return
			}
			_, err := fmt.Fprintf(w, "data: %s\n\n", msg.Payload)
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
