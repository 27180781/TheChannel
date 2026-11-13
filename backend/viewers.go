package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/redis/go-redis/v9"
)

// A channel "locked to authorized viewers" (ChannelData.ViewersOnly) is read
// only by a signed-in account whose email is on the channel's allowlist, or
// that holds a staff role on it (writer and up, super admin included). Having
// the link is not enough, and neither is merely being signed in, which is all
// features.requireAuth asks for.
//
// The flag lives in the channel hash ("viewersOnly"), not in the features
// blob: the features blob belongs to the super admin's screen and is written
// back whole from it, while this setting is the owner's. The allowlist is a
// set of normalised emails beside it.

// maxAllowedViewers caps the allowlist. It is read with SISMEMBER, so its
// size never slows a request, but one form submits it whole.
const maxAllowedViewers = 5000

func allowedViewersKey(slug string) string {
	return fmt.Sprintf("channel:%s:allowed_viewers", slug)
}

// isPrivate is whether the channel is closed to anonymous visitors, for any
// reason. Everything that decides what an outsider may learn about a channel
// (search listing, previews, favicon, mail) asks this, not RequireAuth alone.
func (c *ChannelData) isPrivate() bool {
	return c != nil && (c.Features.RequireAuth || c.ViewersOnly)
}

// viewerAllowed answers whether email (already normalised) may read slug when
// the channel is locked to its allowlist. Staff always may: an owner who
// forgot to list themselves must not lock themselves out of their channel.
func viewerAllowed(ctx context.Context, slug, email string) (bool, error) {
	if email == "" {
		return false, nil
	}
	if emailHasChannelRole(email, slug, RoleWriter) {
		return true, nil
	}
	return rdb.SIsMember(ctx, allowedViewersKey(slug), email).Result()
}

// enforceViewersOnly runs for every /api/channel/{slug}/* request of a locked
// channel. An anonymous visitor gets 401, so the client sends them to sign
// in; a signed-in account that is not allowed gets 403 channel_restricted,
// which the client shows as its own card instead of a login loop.
func enforceViewersOnly(w http.ResponseWriter, r *http.Request, ch *ChannelData) bool {
	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	allowed, err := viewerAllowed(ctx, ch.Slug, normEmail(s.Email))
	if err != nil {
		// Fail closed: a Redis error must not open a locked channel.
		http.Error(w, "error", http.StatusInternalServerError)
		return false
	}
	if !allowed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "channel_restricted"})
		return false
	}
	return true
}

type viewersSettings struct {
	Enabled bool     `json:"enabled"`
	Emails  []string `json:"emails"`
}

// GET /api/channel/{slug}/admin/viewers/get — owner.
func getChannelViewers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := channelFromCtx(r)
	if ch == nil {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}
	emails, err := rdb.SMembers(ctx, allowedViewersKey(ch.Slug)).Result()
	if err != nil && err != redis.Nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	sort.Strings(emails)
	if emails == nil {
		emails = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(viewersSettings{Enabled: ch.ViewersOnly, Emails: emails})
}

// normaliseViewerList lower-cases, trims and de-duplicates the submitted
// list, and refuses anything that is not an email, so a typo is reported
// instead of silently locking a person out.
func normaliseViewerList(in []string) ([]string, error) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		e := normEmail(raw)
		if e == "" {
			continue
		}
		if len(e) > 254 || !looksLikeEmail(e) {
			return nil, fmt.Errorf("invalid email: %s", e)
		}
		if seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	if len(out) > maxAllowedViewers {
		return nil, fmt.Errorf("too many emails (max %d)", maxAllowedViewers)
	}
	sort.Strings(out)
	return out, nil
}

// POST /api/channel/{slug}/admin/viewers/set — owner. Replaces the toggle and
// the whole list in one transaction.
func setChannelViewers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := channelFromCtx(r)
	if ch == nil {
		http.Error(w, "Channel not found", http.StatusNotFound)
		return
	}
	var req viewersSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	emails, err := normaliseViewerList(req.Emails)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	key := allowedViewersKey(ch.Slug)
	flag := "0"
	if req.Enabled {
		flag = "1"
	}
	_, err = rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Del(ctx, key)
		if len(emails) > 0 {
			members := make([]any, len(emails))
			for i, e := range emails {
				members[i] = e
			}
			p.SAdd(ctx, key, members...)
		}
		p.HSet(ctx, "channel:"+ch.Slug, "viewersOnly", flag)
		return nil
	})
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	// A channel that just closed must leave the sitemap now, not when the
	// cache expires.
	if req.Enabled != ch.ViewersOnly {
		resetSitemapCache()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(viewersSettings{Enabled: req.Enabled, Emails: emails})
}

// sseViewerStillAllowed re-checks a live stream's viewer against the channel
// as it is now. Called on each heartbeat, so a removal takes effect within
// one heartbeat interval without a reconnect.
func sseViewerStillAllowed(parent context.Context, slug, email string) bool {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	// One field, not the whole channel: this runs for every open stream of
	// every channel, public ones included.
	flag, err := rdb.HGet(ctx, "channel:"+slug, "viewersOnly").Result()
	if err == redis.Nil || (err == nil && flag != "1") {
		return true
	}
	if err != nil {
		// A transient read error keeps the stream; the next heartbeat asks again.
		return true
	}
	ok, err := viewerAllowed(ctx, slug, email)
	return err == nil && ok
}
