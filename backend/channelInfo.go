package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

type Channel struct {
	Id                      string    `json:"id"`
	Name                    string    `json:"name"`
	Description             string    `json:"description"`
	CreatedAt               time.Time `json:"created_at"`
	LogoUrl                 string    `json:"logoUrl"`
	Views                   int64     `json:"views"`
	RequireAuthForViewFiles bool      `json:"require_auth_for_view_files"`
	ContactUs               string    `json:"contact_us"`
}

func getChannelInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	ch := channelFromCtx(r)

	amount, err := dbGetUsersAmount(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	var channel Channel
	if ch != nil {
		channel.Id = ch.Slug
		channel.Name = ch.Name
		channel.Description = ch.Description
		channel.CreatedAt = ch.CreatedAt
		channel.LogoUrl = ch.LogoUrl
		channel.RequireAuthForViewFiles = ch.Features.RequireAuthFiles
		channel.ContactUs = ch.ContactUs
	}
	channel.Views = amount

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

func editChannelInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	type Request struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		LogoUrl     string `json:"logoUrl"`
		ContactUs   string `json:"contactUs"`
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	hashKey := "channel:" + slug
	if _, err := rdb.HSet(ctx, hashKey, "name", req.Name, "description", req.Description, "logoUrl", req.LogoUrl, "contactUs", req.ContactUs).Result(); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	res := Response{Success: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
