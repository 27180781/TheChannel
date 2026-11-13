package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A channel locked to its allowlist: the list decides who reads it, staff
// always may, and the link or a bare sign-in is not enough.

const (
	vwOwner    = "vw-owner@example.com"
	vwWriter   = "vw-writer@example.com"
	vwListed   = "vw-listed@example.com"
	vwStranger = "vw-stranger@example.com"
)

func newViewersFixture(t *testing.T, slug string) {
	t.Helper()
	ctx := testCtx(t)
	useTestSessionStore(t)
	seedChannel(t, ctx, slug, Settings{})
	privilegesUsers.Store(vwOwner, User{Email: vwOwner, ChannelRoles: map[string]ChannelRole{slug: RoleOwner}})
	privilegesUsers.Store(vwWriter, User{Email: vwWriter, ChannelRoles: map[string]ChannelRole{slug: RoleWriter}})
	t.Cleanup(func() {
		privilegesUsers.Delete(vwOwner)
		privilegesUsers.Delete(vwWriter)
	})
}

// viewAs runs a channel read through channelIfRequireAuth with the channel as
// channelMiddleware loads it. email "" is an anonymous visitor.
func viewAs(t *testing.T, slug, email string) int {
	t.Helper()
	var r *http.Request
	if email == "" {
		r = httptest.NewRequest(http.MethodGet, "/api/channel/"+slug+"/messages", nil)
	} else {
		r = requestAs(t, Session{ID: email, Email: email}, slug, http.MethodGet, "/api/channel/"+slug+"/messages", nil, nil)
	}
	ch, err := dbGetChannel(r.Context(), slug)
	if err != nil {
		t.Fatalf("load channel: %v", err)
	}
	r = r.WithContext(context.WithValue(r.Context(), channelCtxKey, ch))
	rec := httptest.NewRecorder()
	channelIfRequireAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, r)
	return rec.Code
}

func setViewersAs(t *testing.T, slug, email string, body viewersSettings) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := requestAs(t, Session{ID: email, Email: email}, slug, http.MethodPost, "/api/channel/"+slug+"/admin/viewers/set", bytes.NewReader(raw), nil)
	ch, err := dbGetChannel(r.Context(), slug)
	if err != nil {
		t.Fatalf("load channel: %v", err)
	}
	r = r.WithContext(context.WithValue(r.Context(), channelCtxKey, ch))
	rec := httptest.NewRecorder()
	protectedWithChannelRole(RoleOwner, setChannelViewers)(rec, r)
	return rec
}

func TestViewersOnlyGatesByAllowlist(t *testing.T) {
	slug := "vw-test-gate"
	newViewersFixture(t, slug)

	// Unlocked: open to anyone, as before.
	if got := viewAs(t, slug, ""); got != http.StatusOK {
		t.Fatalf("public channel, anonymous: got %d", got)
	}

	rec := setViewersAs(t, slug, vwOwner, viewersSettings{Enabled: true, Emails: []string{"  VW-Listed@Example.com ", vwListed}})
	if rec.Code != http.StatusOK {
		t.Fatalf("owner set: %d %s", rec.Code, rec.Body)
	}
	var saved viewersSettings
	json.Unmarshal(rec.Body.Bytes(), &saved)
	if len(saved.Emails) != 1 || saved.Emails[0] != vwListed {
		t.Fatalf("list not normalised and deduplicated: %v", saved.Emails)
	}

	cases := []struct {
		who  string
		want int
	}{
		{"", http.StatusUnauthorized},
		{vwStranger, http.StatusForbidden},
		{vwListed, http.StatusOK},
		{vwWriter, http.StatusOK},
		{vwOwner, http.StatusOK},
	}
	for _, c := range cases {
		if got := viewAs(t, slug, c.who); got != c.want {
			t.Errorf("locked channel, %q: got %d, want %d", c.who, got, c.want)
		}
	}

	ch, _ := dbGetChannel(testCtx(t), slug)
	if !ch.isPrivate() || channelSearchListed(ch) {
		t.Errorf("a locked channel must count as private and stay out of search")
	}

	// Unlocking reopens it; the list is kept for next time.
	if rec := setViewersAs(t, slug, vwOwner, viewersSettings{Enabled: false, Emails: []string{vwListed}}); rec.Code != http.StatusOK {
		t.Fatalf("owner unlock: %d", rec.Code)
	}
	if got := viewAs(t, slug, vwStranger); got != http.StatusOK {
		t.Errorf("unlocked channel, stranger: got %d", got)
	}
}

func TestViewersOnlyRemovalDropsLiveStream(t *testing.T) {
	slug := "vw-test-sse"
	newViewersFixture(t, slug)
	ctx := testCtx(t)
	setViewersAs(t, slug, vwOwner, viewersSettings{Enabled: true, Emails: []string{vwListed}})
	if !sseViewerStillAllowed(ctx, slug, vwListed) {
		t.Fatal("listed viewer dropped")
	}
	setViewersAs(t, slug, vwOwner, viewersSettings{Enabled: true, Emails: []string{}})
	if sseViewerStillAllowed(ctx, slug, vwListed) {
		t.Error("removed viewer kept the stream")
	}
	if !sseViewerStillAllowed(ctx, slug, vwWriter) {
		t.Error("staff dropped from a locked channel")
	}
}

func TestSetViewersRejects(t *testing.T) {
	slug := "vw-test-reject"
	newViewersFixture(t, slug)
	if rec := setViewersAs(t, slug, vwOwner, viewersSettings{Enabled: true, Emails: []string{"not-an-email"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad email: got %d", rec.Code)
	}
	// Only the owner manages the list.
	if rec := setViewersAs(t, slug, vwWriter, viewersSettings{Enabled: true, Emails: []string{vwWriter}}); rec.Code != http.StatusForbidden {
		t.Errorf("writer set: got %d", rec.Code)
	}
	ch, _ := dbGetChannel(testCtx(t), slug)
	if ch.ViewersOnly {
		t.Error("a refused request locked the channel")
	}
}
