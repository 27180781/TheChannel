package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fetchPublicChannels(t *testing.T) []PublicChannelCard {
	t.Helper()
	rec := httptest.NewRecorder()
	getPublicChannels(rec, httptest.NewRequest(http.MethodGet, "/api/public/channels", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/public/channels: %d", rec.Code)
	}
	var resp struct {
		Channels []PublicChannelCard `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if resp.Channels == nil {
		t.Fatalf("channels must be a list, got %s", rec.Body.String())
	}
	return resp.Channels
}

func hasCard(cards []PublicChannelCard, slug string) *PublicChannelCard {
	for i := range cards {
		if cards[i].Slug == slug {
			return &cards[i]
		}
	}
	return nil
}

// Opt-in only: a new channel is not listed until its managers tick the box,
// and a private or disabled one never is.
func TestPublicChannelsDirectory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resetSitemapCache()

	seedPreviewChannel(t, ctx, "dir-open", ChannelData{Name: "ערוץ פתוח", Description: "על הכול", LogoUrl: "/logo.png", Features: defaultChannelFeatures()})
	private := defaultChannelFeatures()
	private.RequireAuth = true
	seedPreviewChannel(t, ctx, "dir-private", ChannelData{Name: "פרטי", Features: private})
	seedPreviewChannel(t, ctx, "dir-locked", ChannelData{Name: "נעול", Features: defaultChannelFeatures()})
	if err := rdb.HSet(ctx, "channel:dir-locked", "viewersOnly", "1").Err(); err != nil {
		t.Fatal(err)
	}
	gatedFiles := defaultChannelFeatures()
	gatedFiles.RequireAuthFiles = true
	seedPreviewChannel(t, ctx, "dir-gated-logo", ChannelData{Name: "לוגו נעול", LogoUrl: "/x.png", Features: gatedFiles})

	if hasCard(fetchPublicChannels(t), "dir-open") != nil {
		t.Fatal("a channel must not be listed before its managers opt in")
	}
	if ch, _ := dbGetChannel(ctx, "dir-open"); ch.PublicListed {
		t.Fatal("publicListed must default to false")
	}

	for _, slug := range []string{"dir-open", "dir-private", "dir-locked", "dir-gated-logo"} {
		callEditChannelInfo(t, slug, `{"publicListed":true}`, http.StatusOK)
	}
	cards := fetchPublicChannels(t)
	open := hasCard(cards, "dir-open")
	if open == nil || open.Name != "ערוץ פתוח" || open.Description != "על הכול" || open.LogoUrl != "/logo.png" {
		t.Fatalf("listed channel card: %+v", open)
	}
	if hasCard(cards, "dir-private") != nil {
		t.Error("a sign-in-only channel must not be listed")
	}
	if hasCard(cards, "dir-locked") != nil {
		t.Error("a channel locked to its allowlist must not be listed")
	}
	if g := hasCard(cards, "dir-gated-logo"); g == nil || g.LogoUrl != "" {
		t.Errorf("a logo behind the files gate must be left off the card: %+v", g)
	}

	// The server-rendered page lists it too.
	withSpaFolder(t, "", "")
	page := spaPage(t, "/channels")
	mustContain(t, page, `<h1>ערוצים ציבוריים</h1>`, `<a href="/channel/dir-open">ערוץ פתוח</a>`, `<link rel="canonical" href="https://example.test/channels">`)
	if strings.Contains(page, "dir-private") {
		t.Error("the static page lists a private channel")
	}

	// Unticking takes it off at once, without waiting for the cache.
	callEditChannelInfo(t, "dir-open", `{"publicListed":false}`, http.StatusOK)
	if hasCard(fetchPublicChannels(t), "dir-open") != nil {
		t.Fatal("an unlisted channel is still in the directory")
	}
}
