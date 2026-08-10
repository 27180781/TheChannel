package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi"
)

func addNewPost(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !slugRegex.MatchString(slug) {
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg := getChannelConfig(ctx, slug)
	key := r.Header.Get("X-API-Key")
	if key != cfg.ApiSecretKey || cfg.ApiSecretKey == "" {
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	var message Message
	var err error
	defer r.Body.Close()

	body := Message{}
	if err = json.NewDecoder(r.Body).Decode(&body); err != nil {
		log.Printf("Failed to decode message: %v\n", err)
		http.Error(w, "error", http.StatusBadRequest)
		return
	}

	if message.ID, err = getMessageNextId(ctx, slug); err != nil {
		log.Printf("Failed to allocate message id: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	message.Type = "md" //body.Type
	message.Author = body.Author
	// The timestamp is the sort score. A missing one decodes to the zero time
	// and buries the post at the bottom of every listing; a far-future one pins
	// it to the top forever. Backdated imports stay legitimate.
	if body.Timestamp.IsZero() {
		message.Timestamp = time.Now()
	} else if body.Timestamp.After(time.Now().Add(time.Hour)) {
		http.Error(w, "timestamp out of range", http.StatusBadRequest)
		return
	} else {
		message.Timestamp = body.Timestamp
	}
	message.Text = body.Text
	message.Views = 0
	message.IsAds = body.IsAds

	if err = setMessage(ctx, slug, &message, false); err != nil {
		log.Printf("Failed to set new message: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(message)
}
