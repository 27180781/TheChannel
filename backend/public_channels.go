package main

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// The public channels directory: a page on the site (/channels) that lists
// every channel whose managers asked for it, each with a link to open it.
// Opt-in only. The flag is "publicListed" in the channel hash, absent (= not
// listed) for every channel created before it existed and for every new one.

// publicChannelsPath is the directory's address in the app and on the server.
const publicChannelsPath = "/channels"

// publicChannelsCacheTTL bounds how often the directory walks the channel
// list. A manager's own toggle drops the cache at once (resetSitemapCache),
// so the TTL only matters for changes made elsewhere.
const publicChannelsCacheTTL = 5 * time.Minute

// maxPublicChannelDescLen is how much of a channel's description a card
// carries; the card clamps it further.
const maxPublicChannelDescLen = 300

// PublicChannelCard is one card on the directory page. Only what the channel
// page itself already shows any visitor.
type PublicChannelCard struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	LogoUrl     string `json:"logoUrl"`
}

// channelDirectoryListed is whether a channel appears in the directory: its
// managers opted in, and a stranger can actually read it (not disabled, not
// sign-in only, not locked to its allowlist; see isPrivate). A channel the
// directory would only send people to a locked door is left out rather than
// marked.
func channelDirectoryListed(ch *ChannelData) bool {
	return ch != nil && ch.PublicListed && !ch.Features.Disabled && !ch.isPrivate() &&
		isPlausibleSlug(ch.Slug)
}

func publicChannelCardOf(ch *ChannelData) PublicChannelCard {
	name := strings.TrimSpace(ch.Name)
	if name == "" {
		name = ch.Slug
	}
	c := PublicChannelCard{
		Slug:        ch.Slug,
		Name:        name,
		Description: truncateRunes(strings.TrimSpace(ch.Description), maxPublicChannelDescLen),
	}
	// A logo behind the files gate would be a broken image for a visitor who
	// is not signed in; the card shows the initial instead.
	if logo := strings.TrimSpace(ch.LogoUrl); logo != "" && !ch.Features.RequireAuthFiles && isSafeLogoURL(logo) {
		c.LogoUrl = logo
	}
	return c
}

var publicChannelsCache struct {
	sync.Mutex
	built time.Time
	cards []PublicChannelCard
}

// listPublicChannels returns the directory, sorted by name. Never nil.
func listPublicChannels(ctx context.Context) ([]PublicChannelCard, error) {
	publicChannelsCache.Lock()
	defer publicChannelsCache.Unlock()
	if publicChannelsCache.cards != nil && time.Since(publicChannelsCache.built) < publicChannelsCacheTTL {
		return publicChannelsCache.cards, nil
	}
	channels, err := dbListChannels(ctx)
	if err != nil {
		return nil, err
	}
	cards := make([]PublicChannelCard, 0)
	for _, ch := range channels {
		if channelDirectoryListed(ch) {
			cards = append(cards, publicChannelCardOf(ch))
		}
	}
	sort.SliceStable(cards, func(i, j int) bool {
		return strings.ToLower(cards[i].Name) < strings.ToLower(cards[j].Name)
	})
	publicChannelsCache.cards = cards
	publicChannelsCache.built = time.Now()
	return cards, nil
}

func resetPublicChannelsCache() {
	publicChannelsCache.Lock()
	publicChannelsCache.cards = nil
	publicChannelsCache.Unlock()
}

// GET /api/public/channels
//
// No login: the directory is for visitors.
func getPublicChannels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cards, err := listPublicChannels(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	json.NewEncoder(w).Encode(map[string]any{"channels": cards})
}

const (
	publicChannelsTitle = "ערוצים ציבוריים"
	publicChannelsLead  = "ערוצי עדכונים פתוחים לכולם: בוחרים ערוץ ונכנסים לקרוא, בלי אפליקציה ובלי הרשמה."
)

// publicChannelsStaticBody is the directory as plain HTML, for crawlers and
// for the moment before the app starts.
func publicChannelsStaticBody(siteName string, cards []PublicChannelCard) string {
	var b strings.Builder
	b.WriteString(staticShellStyle)
	b.WriteString(`<main class="ssr">`)
	b.WriteString("<h1>" + html.EscapeString(publicChannelsTitle) + "</h1>")
	b.WriteString("<p>" + html.EscapeString(publicChannelsLead) + "</p>")
	if len(cards) == 0 {
		b.WriteString("<p>עדיין אין כאן ערוצים.</p>")
	} else {
		b.WriteString("<ul>")
		for _, c := range cards {
			b.WriteString(`<li><a href="/channel/` + html.EscapeString(c.Slug) + `">` + html.EscapeString(c.Name) + "</a>")
			if c.Description != "" {
				b.WriteString(" · " + html.EscapeString(c.Description))
			}
			b.WriteString("</li>")
		}
		b.WriteString("</ul>")
	}
	b.WriteString(`<p><a href="/">` + html.EscapeString(siteName) + "</a></p>")
	b.WriteString("</main>")
	return b.String()
}
