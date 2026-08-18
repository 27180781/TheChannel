package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	// maxChannelsPerOwner caps self-service creation per account. The endpoint is
	// open to every logged-in user, so without a cap one account can mint
	// channels (and their Redis keyspace) without bound.
	maxChannelsPerOwner = 5
	// Field caps on the stored channel record.
	maxChannelNameLen = 100
	maxChannelDescLen = 2000
)

// reservedSlugs are the top-level frontend route paths (see
// frontend/src/app/app.routes.ts) plus the prefixes the backend serves itself.
// A channel taking one of these would be unreachable at /<slug> and would
// shadow the real page.
var reservedSlugs = map[string]struct{}{
	"login":       {},
	"channel":     {},
	"channels":    {},
	"super-admin": {},
	"admin":       {},
	"api":         {},
	"assets":      {},
	"auth":        {},
	"static":      {},
}

func isReservedSlug(slug string) bool {
	_, ok := reservedSlugs[slug]
	return ok
}

// slugAvailability reports why a slug cannot be used, or "" when it is free.
// Kept separate from the handlers so both the create and the live-check
// endpoint answer identically.
func slugAvailability(ctx context.Context, slug string) (string, error) {
	if !slugRegex.MatchString(slug) {
		return "invalid", nil
	}
	if isReservedSlug(slug) {
		return "reserved", nil
	}
	exists, err := dbChannelExists(ctx, slug)
	if err != nil {
		return "", err
	}
	if exists {
		return "taken", nil
	}
	return "", nil
}

// countOwnedChannels counts the channels the email holds RoleOwner on. The
// users:list blob is the same source dbAssignChannelRole writes, so a channel
// created a moment ago is already counted.
func countOwnedChannels(ctx context.Context, email string) (int, error) {
	users, err := dbGetUsersList(ctx)
	if err != nil {
		return 0, err
	}
	for _, u := range users {
		if u.Email != email {
			continue
		}
		var n int
		for _, role := range u.ChannelRoles {
			if role == RoleOwner {
				n++
			}
		}
		return n, nil
	}
	return 0, nil
}

// sessionEmail returns the authenticated email carried by the session cookie.
// Self-service creation must take the owner from here and never from the
// request body: an email accepted from the body would let anyone squat a slug
// and hand ownership to somebody else's address.
func sessionEmail(r *http.Request) (Session, bool) {
	session, _ := store.Get(r, cookieName)
	s, ok := session.Values["user"].(Session)
	if !ok || s.Email == "" {
		return Session{}, false
	}
	return s, true
}

// sessionDisplayName is the human label recorded on the audit trail.
func sessionDisplayName(s Session) string {
	if s.PublicName != "" {
		return s.PublicName
	}
	if s.Username != "" {
		return s.Username
	}
	return s.Email
}

// POST /api/channels/create (authenticated)
//
// The only channel creation path: a logged-in user gets their channel
// instantly. The super admin's channel-requests screen still serves the records
// written below (plus any request left over from the removed public form).
func createChannelSelfService(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	// The creation throttle is spent further down, immediately before the channel
	// is written. At three per hour, charging quota for a rejected slug meant
	// three typos locked the account out for twenty minutes having created nothing.
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()

	var body struct {
		Slug        string `json:"slug"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if body.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if len(body.Name) > maxChannelNameLen || len(body.Description) > maxChannelDescLen {
		http.Error(w, "field too long", http.StatusBadRequest)
		return
	}

	reason, err := slugAvailability(ctx, body.Slug)
	if err != nil {
		http.Error(w, "error checking slug", http.StatusInternalServerError)
		return
	}
	switch reason {
	case "invalid":
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	case "reserved":
		http.Error(w, "slug is reserved", http.StatusBadRequest)
		return
	case "taken":
		http.Error(w, "slug already taken", http.StatusConflict)
		return
	}

	// Serialise creates by the same owner. The cap below is a check-then-act:
	// countOwnedChannels reads the roles list, but the role that would make this
	// channel count is only assigned after dbCreateChannel further down. Three
	// concurrent requests (which the creation limiter's burst of 3 allows) all
	// read the same count, all pass, and all create — an account holding five
	// channels ends up with seven. dbCreateChannel's atomic slug claim does not
	// help, because the slugs are different.
	//
	// The lock is short-lived and owner-scoped, so it costs nothing to anyone
	// else and cannot outlive a crashed request.
	createLock := "channel_create:lock:" + strings.ToLower(s.Email)
	gotLock, lockErr := rdb.SetNX(ctx, createLock, 1, 15*time.Second).Result()
	if lockErr != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if !gotLock {
		http.Error(w, "another channel is already being created for this account", http.StatusConflict)
		return
	}
	defer rdb.Del(ctx, createLock)

	owned, err := countOwnedChannels(ctx, s.Email)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if owned >= maxChannelsPerOwner {
		http.Error(w, "channel limit reached for this account", http.StatusForbidden)
		return
	}

	// Validation is done and nothing has been persisted yet, so this is the first
	// point where the request actually costs a channel.
	if !allowOrRetryAfter(w, channelCreateLimiter(s.Email), "too many requests — please try again later") {
		return
	}

	channel := &ChannelData{
		Slug:        body.Slug,
		Name:        body.Name,
		Description: body.Description,
		OwnerEmail:  s.Email,
		CreatedAt:   time.Now(),
		Features:    defaultChannelFeatures(),
	}

	if err := dbCreateChannel(ctx, channel); err != nil {
		// dbCreateChannel claims the slug atomically, so this is the branch that
		// actually decides a race between two concurrent creates.
		if errors.Is(err, errChannelExists) {
			http.Error(w, "slug already taken", http.StatusConflict)
			return
		}
		http.Error(w, "error creating channel", http.StatusInternalServerError)
		return
	}

	dbAssignChannelRole(ctx, s.Email, channel.Slug, RoleOwner)
	if err := initializePrivilegeUsers(); err != nil {
		log.Printf("initializePrivilegeUsers after createChannelSelfService(%s): %v", channel.Slug, err)
	}

	// Recorded as an already-approved request so the super admin's channel
	// requests screen becomes an audit log of self-service creations instead of
	// going empty now that this is the primary path.
	req := &ChannelRequest{
		ID:           generatedRandomID(12),
		Name:         sessionDisplayName(s),
		Email:        s.Email,
		DesiredSlug:  channel.Slug,
		Description:  channel.Description,
		Status:       RequestStatusApproved,
		Notes:        "self-service creation: " + channel.Name,
		ApprovedSlug: channel.Slug,
		CreatedAt:    channel.CreatedAt,
	}
	if err := dbSaveChannelRequest(ctx, req); err != nil {
		// The channel exists and is usable; losing the audit record must not fail
		// the creation the user already paid for.
		log.Printf("save self-service channel request record for %s: %v", channel.Slug, err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{
		"slug": channel.Slug,
		"name": channel.Name,
	})
}

// GET /api/channels/slug-available?slug=<slug> (authenticated)
// Live feedback for the create form.
func checkSlugAvailable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "User not authenticated", http.StatusUnauthorized)
		return
	}

	// Stays at the top: this endpoint's whole cost IS the lookup, so there is no
	// later point to move it to. Retry-After lets the form back off precisely
	// instead of leaving the live check silently stuck.
	if !allowOrRetryAfter(w, slugCheckLimiter(s.Email), "too many requests — please try again later") {
		return
	}

	reason, err := slugAvailability(ctx, r.URL.Query().Get("slug"))
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"available": reason == "",
		"reason":    reason,
	})
}
