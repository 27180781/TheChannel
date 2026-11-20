package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestPickNudgeRules(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	at := func(d time.Duration) *time.Time { t := ago(d); return &t }
	day := 24 * time.Hour
	none := nudgeState{sent: map[string]time.Time{}}

	cases := []struct {
		name string
		a    channelActivity
		st   nudgeState
		want string
	}{
		{"brand new, nothing posted: too early", channelActivity{CreatedAt: ago(day)}, none, ""},
		{"two days, nothing posted", channelActivity{CreatedAt: ago(2*day + time.Hour)}, none, nudgeNoMessages},
		{"nothing posted, already reminded", channelActivity{CreatedAt: ago(10 * day)},
			nudgeState{sent: map[string]time.Time{nudgeNoMessages: ago(6 * day)}, last: ago(6 * day)}, ""},
		{"abandoned for months: left alone", channelActivity{CreatedAt: ago(200 * day)}, none, ""},
		{"posting, but only the owner reads", channelActivity{CreatedAt: ago(5 * day), Messages: 3, LastMessageAt: at(day), Subscribers: 1, Owners: 1}, none, nudgeNoSubscribers},
		{"posting, readers came", channelActivity{CreatedAt: ago(5 * day), Messages: 3, LastMessageAt: at(day), Subscribers: 4, Owners: 1}, none, ""},
		{"quiet for two weeks", channelActivity{CreatedAt: ago(60 * day), Messages: 30, LastMessageAt: at(15 * day), Subscribers: 40, Owners: 1}, none, nudgeInactive},
		{"quiet, reminded ten days ago", channelActivity{CreatedAt: ago(60 * day), Messages: 30, LastMessageAt: at(20 * day), Subscribers: 40, Owners: 1},
			nudgeState{sent: map[string]time.Time{nudgeInactive: ago(10 * day)}, last: ago(10 * day)}, ""},
		{"quiet for half a year: left alone", channelActivity{CreatedAt: ago(400 * day), Messages: 30, LastMessageAt: at(180 * day)}, none, ""},
		{"any reminder in the last three days blocks the next", channelActivity{CreatedAt: ago(5 * day), Messages: 1, LastMessageAt: at(day), Subscribers: 1, Owners: 1},
			nudgeState{sent: map[string]time.Time{nudgeNoMessages: ago(2 * day)}, last: ago(2 * day)}, ""},
		{"owner turned them off", channelActivity{CreatedAt: ago(3 * day)}, nudgeState{sent: map[string]time.Time{}, off: true}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, _ := pickNudge(now, &c.a, c.st)
			if got != c.want {
				t.Fatalf("pickNudge = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPickNudgeInactiveKeyFollowsTheLastPost(t *testing.T) {
	now := time.Now()
	last := now.Add(-15 * 24 * time.Hour)
	a := &channelActivity{CreatedAt: now.Add(-90 * 24 * time.Hour), Messages: 5, LastMessageAt: &last, Subscribers: 9, Owners: 1}
	_, k1, days := pickNudge(now, a, nudgeState{sent: map[string]time.Time{}})
	if days != 15 {
		t.Fatalf("days = %d, want 15", days)
	}
	later := last.Add(time.Hour)
	a.LastMessageAt = &later
	_, k2, _ := pickNudge(now, a, nudgeState{sent: map[string]time.Time{}})
	if k1 == k2 {
		t.Fatalf("a new quiet spell must be a new key, got %q twice", k1)
	}
}

func TestNudgeOptOutToken(t *testing.T) {
	prev := magnetMailSecret
	t.Cleanup(func() { magnetMailSecret = prev })
	magnetMailSecret = "a"
	a := nudgeOptOutToken("chan-one")
	if a == nudgeOptOutToken("chan-two") {
		t.Fatal("two channels share a token")
	}
	magnetMailSecret = "b"
	if a == nudgeOptOutToken("chan-one") {
		t.Fatal("the token does not depend on the secret")
	}
}

func TestChannelRemindersOffLink(t *testing.T) {
	ctx := context.Background()
	useFakeMagnet(t, nil)
	slug := "nudge-off-link"
	seedPreviewChannel(t, ctx, slug, ChannelData{Name: "ערוץ"})
	t.Cleanup(func() { dbDeleteChannel(context.Background(), slug) })

	call := func(q url.Values) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		channelRemindersOff(w, httptest.NewRequest(http.MethodGet, "/api/channel-reminders/off?"+q.Encode(), nil))
		return w
	}
	if w := call(url.Values{"c": {slug}, "t": {"0000"}}); w.Code != http.StatusBadRequest {
		t.Fatalf("forged token: %d, want 400", w.Code)
	}
	if off, _ := rdb.HGet(ctx, nudgesKey(slug), "off").Result(); off == "1" {
		t.Fatal("a forged link turned the mails off")
	}
	u, _ := url.Parse(nudgeOptOutURL(publicBaseURL(), slug))
	w := call(u.Query())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "התזכורות כובו") {
		t.Fatalf("signed link: %d %s", w.Code, w.Body.String())
	}
	if off, _ := rdb.HGet(ctx, nudgesKey(slug), "off").Result(); off != "1" {
		t.Fatal("the signed link did not turn the mails off")
	}
}

func nudgeHitsFor(f *fakeMagnet, slug string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, h := range f.hits {
		if h.body["event"] != magnetMailEventChannelNudge {
			continue
		}
		if ch, ok := h.body["channel"].(map[string]any); ok && ch["slug"] == slug {
			out = append(out, h.body)
		}
	}
	return out
}

func TestRunNudgesMailsTheOwnerOnce(t *testing.T) {
	ctx := context.Background()
	useTestSessionStore(t)
	f := useFakeMagnet(t, nil)
	email, slug := "nudge-owner@example.com", "nudge-run-once"
	if w := createChannelAs(t, email, slug); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	magnetMailInFlight.Wait()
	rdb.Del(ctx, nudgesKey(slug))
	// Opened three days ago, nothing posted since.
	rdb.HSet(ctx, "channel:"+slug, "createdAt", time.Now().Add(-72*time.Hour).Format(time.RFC3339))

	runNudges(ctx)
	hits := nudgeHitsFor(f, slug)
	if len(hits) != 1 {
		t.Fatalf("got %d reminder mails, want 1", len(hits))
	}
	body := hits[0]
	if body["to"] != email {
		t.Fatalf("to = %v, want the owner", body["to"])
	}
	n := obj(t, body, "nudge")
	if n["kind"] != nudgeNoMessages {
		t.Fatalf("kind = %v", n["kind"])
	}
	if !strings.HasPrefix(str(t, n, "opt_out_url"), defaultPublicBaseURL+"/api/channel-reminders/off?") ||
		!strings.HasPrefix(str(t, n, "share_url"), defaultPublicBaseURL+"/channel/"+slug) {
		t.Fatalf("links: %v", n)
	}
	if str(t, body, "idempotency_key") != "channel_nudge:"+slug+":"+nudgeNoMessages {
		t.Fatalf("key = %v", body["idempotency_key"])
	}

	runNudges(ctx)
	if got := len(nudgeHitsFor(f, slug)); got != 1 {
		t.Fatalf("a second pass sent again: %d mails", got)
	}
}

func TestRunNudgesSkipsOptedOutAndFailedSendIsNotRecorded(t *testing.T) {
	ctx := context.Background()
	useTestSessionStore(t)
	f := useFakeMagnet(t, func(n int, w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	email, slug := "nudge-owner2@example.com", "nudge-run-off"
	if w := createChannelAs(t, email, slug); w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	magnetMailInFlight.Wait()
	rdb.Del(ctx, nudgesKey(slug))
	rdb.HSet(ctx, "channel:"+slug, "createdAt", time.Now().Add(-72*time.Hour).Format(time.RFC3339))

	runNudges(ctx)
	if len(nudgeHitsFor(f, slug)) != 1 {
		t.Fatal("expected one attempt")
	}
	if sent, _ := rdb.HGet(ctx, nudgesKey(slug), nudgeNoMessages).Result(); sent != "" {
		t.Fatal("a refused mail was recorded as sent")
	}

	setNudgeMailsOff(ctx, slug, true)
	runNudges(ctx)
	if len(nudgeHitsFor(f, slug)) != 1 {
		t.Fatal("an opted-out channel was mailed")
	}
}

func TestChannelActivityCounts(t *testing.T) {
	ctx := context.Background()
	useTestSessionStore(t)
	useFakeMagnet(t, nil)
	email, slug := "activity-owner@example.com", "activity-counts"
	if w := createChannelAs(t, email, slug); w.Code != http.StatusCreated {
		t.Fatalf("create: %d", w.Code)
	}
	magnetMailInFlight.Wait()
	rdb.SAdd(ctx, "channel:"+slug+":registered_emails", "a@example.com", "b@example.com")
	rdb.ZAdd(ctx, "channel:"+slug+":m_times", redisZ(time.Now().Add(-time.Hour), "channel:"+slug+":messages:1"))
	// A post scheduled for tomorrow does not count as activity today.
	rdb.ZAdd(ctx, "channel:"+slug+":m_times", redisZ(time.Now().Add(24*time.Hour), "channel:"+slug+":messages:2"))

	ch, err := dbGetChannel(ctx, slug)
	if err != nil {
		t.Fatal(err)
	}
	a, err := loadChannelActivity(ctx, ch)
	if err != nil {
		t.Fatal(err)
	}
	if a.Messages != 2 || a.Subscribers != 2 || a.Owners != 1 || a.Team != 0 {
		raw, _ := json.Marshal(a)
		t.Fatalf("activity = %s", raw)
	}
	if a.LastMessageAt == nil || a.LastMessageAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("lastMessageAt = %v, want clamped to now", a.LastMessageAt)
	}
}

func redisZ(t time.Time, member string) redis.Z {
	return redis.Z{Score: float64(t.Unix()), Member: member}
}
