package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// PublicFeatures is the subset of ChannelFeatures the UI needs to know about:
// exactly the toggles requireFeature gates a user-visible action on. Without
// them the client offers a button the backend then answers with 403.
type PublicFeatures struct {
	Reactions         bool `json:"reactions"`
	FileUploads       bool `json:"fileUploads"`
	Reports           bool `json:"reports"`
	ScheduledMessages bool `json:"scheduledMessages"`
}

type Channel struct {
	Id                      string         `json:"id"`
	Name                    string         `json:"name"`
	Description             string         `json:"description"`
	CreatedAt               time.Time      `json:"created_at"`
	LogoUrl                 string         `json:"logoUrl"`
	Views                   int64          `json:"views"`
	RequireAuthForViewFiles bool           `json:"require_auth_for_view_files"`
	ContactUs               string         `json:"contact_us"`
	Features                PublicFeatures `json:"features"`
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
		channel.Features = PublicFeatures{
			Reactions:         ch.Features.Reactions,
			FileUploads:       ch.Features.FileUploads,
			Reports:           ch.Features.Reports,
			ScheduledMessages: ch.Features.ScheduledMessages,
		}
	}
	channel.Views = amount

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(channel)
}

// isSafeLogoURL accepts an empty value, a relative path (what the upload flow
// produces, e.g. /api/channel/<slug>/files/<id>), or an http(s) URL. Anything
// carrying another scheme is rejected, since the value is reflected into an
// <img src> for every visitor.
func isSafeLogoURL(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return true
	}
	// A scheme is "<letters>:" at the very start; a relative path has none.
	if i := strings.IndexByte(u, ':'); i >= 0 {
		scheme := u[:i]
		// A "/" or "?" or "#" before the colon means it is a path, not a scheme
		// (e.g. "/a:b" is a relative path whose first segment contains a colon).
		if !strings.ContainsAny(scheme, "/?#") {
			lower := strings.ToLower(scheme)
			return lower == "http" || lower == "https"
		}
	}
	return true
}

func editChannelInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	// Every field is a pointer so an ABSENT field can be told apart from a
	// deliberately cleared one — only a field the client actually sent is
	// written. Previously name/description/logoUrl were plain strings and always
	// written, so any partial request (a client that omitted a field, or a form
	// that opened before the channel had loaded) blanked the stored value. That
	// is what left a channel with an empty name whose /info then came back with
	// every field empty.
	type Request struct {
		Name        *string `json:"name"`
		Description *string `json:"description"`
		LogoUrl     *string `json:"logoUrl"`
		ContactUs   *string `json:"contactUs"`
	}

	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// A channel must keep a name: refuse to blank it. Description, logo and
	// contact can be cleared, and an omitted field is simply left as-is.
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		http.Error(w, "channel name cannot be empty", http.StatusBadRequest)
		return
	}
	// The logo is reflected to every visitor as an <img src>. A relative path
	// (what the upload flow produces) or an http(s) URL is fine; anything with
	// another scheme — javascript:, data: — is refused.
	if req.LogoUrl != nil && !isSafeLogoURL(*req.LogoUrl) {
		http.Error(w, "invalid logo URL", http.StatusBadRequest)
		return
	}

	hashKey := "channel:" + slug
	args := []any{}
	if req.Name != nil {
		args = append(args, "name", *req.Name)
	}
	if req.Description != nil {
		args = append(args, "description", *req.Description)
	}
	if req.LogoUrl != nil {
		args = append(args, "logoUrl", *req.LogoUrl)
	}
	if req.ContactUs != nil {
		args = append(args, "contactUs", *req.ContactUs)
	}
	if len(args) == 0 {
		// Nothing to change; do not issue an empty HSet.
		res := Response{Success: true}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
		return
	}
	if _, err := rdb.HSet(ctx, hashKey, args...).Result(); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	res := Response{Success: true}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
