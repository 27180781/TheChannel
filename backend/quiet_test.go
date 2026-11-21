package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// Shabbat and Yom Tov (quiet.go): inside a window nothing goes to Magnet and
// nothing is lost; after it, what was held goes out once.

func useQuietWindow(t *testing.T, now time.Time, ws []quietWindow) {
	t.Helper()
	prevNow := quietNow
	quietMu.RLock()
	prevWs := quietWindows
	quietMu.RUnlock()
	quietNow = func() time.Time { return now }
	setQuietWindows(ws)
	ctx := context.Background()
	rdb.Del(ctx, quietHeldMailKey, quietHeldPushKey)
	t.Cleanup(func() {
		quietNow = prevNow
		setQuietWindows(prevWs)
		rdb.Del(context.Background(), quietHeldMailKey, quietHeldPushKey)
	})
}

var testShabbat = quietWindow{
	StartsAt: time.Date(2026, 11, 27, 13, 54, 0, 0, time.UTC),
	EndsAt:   time.Date(2026, 11, 28, 15, 19, 0, 0, time.UTC),
	Label:    "שבת",
}

func TestQuietUntil(t *testing.T) {
	useQuietWindow(t, time.Now(), []quietWindow{testShabbat})
	if _, q := quietUntil(testShabbat.StartsAt.Add(-time.Second)); q {
		t.Error("a second before the window is not quiet")
	}
	if end, q := quietUntil(testShabbat.StartsAt); !q || !end.Equal(testShabbat.EndsAt) {
		t.Errorf("the window's first second: quiet=%v end=%v", q, end)
	}
	if _, q := quietUntil(testShabbat.EndsAt); q {
		t.Error("the window's end is outside it")
	}
}

func TestQuietMailIsHeldThenSentOnce(t *testing.T) {
	f := useFakeMagnet(t, nil)
	useQuietWindow(t, testShabbat.StartsAt.Add(time.Hour), []quietWindow{testShabbat})
	ev, _ := channelCreatedMail(&ChannelData{Slug: "quiet-slug", Name: "x"}, "o@example.com", "")
	if err := deliverMagnetMail(context.Background(), ev); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if n := f.count(); n != 0 {
		t.Fatalf("magnet got %d requests inside the window, want 0", n)
	}
	if n, _ := rdb.ZCard(context.Background(), quietHeldMailKey).Result(); n != 1 {
		t.Fatalf("held mails = %d, want 1", n)
	}

	// Still inside: nothing is released.
	releaseHeld(context.Background())
	magnetMailInFlight.Wait()
	if f.count() != 0 {
		t.Fatal("released inside the window")
	}

	quietNow = func() time.Time { return testShabbat.EndsAt.Add(time.Minute) }
	releaseHeld(context.Background())
	releaseHeld(context.Background())
	magnetMailInFlight.Wait()
	if n := f.count(); n != 1 {
		t.Fatalf("magnet got %d requests after the window, want 1", n)
	}
	if got := str(t, f.last(t).body, "idempotency_key"); got != ev.IdempotencyKey {
		t.Errorf("idempotency_key = %q, want %q", got, ev.IdempotencyKey)
	}
}

func TestQuietMagnetRefusalIsHeld(t *testing.T) {
	f := useFakeMagnet(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":"quiet_window","quiet_until":"2026-11-28T15:19:00.000Z"}`))
	})
	useQuietWindow(t, time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC), nil)
	ev, _ := channelCreatedMail(&ChannelData{Slug: "quiet-refused", Name: "x"}, "o@example.com", "")
	if err := deliverMagnetMail(context.Background(), ev); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if n := f.count(); n != 1 {
		t.Errorf("attempts = %d, want 1 (a quiet refusal is not retried)", n)
	}
	score, err := rdb.ZScore(context.Background(), quietHeldMailKey, string(mustJSON(t, ev))).Result()
	if err != nil || int64(score) != testShabbat.EndsAt.Unix() {
		t.Errorf("held until %v (%v), want %v", int64(score), err, testShabbat.EndsAt.Unix())
	}
}

func TestQuietSupportActivityIsNotHeld(t *testing.T) {
	f := useFakeMagnet(t, nil)
	useQuietWindow(t, testShabbat.StartsAt.Add(time.Hour), []quietWindow{testShabbat})
	ev := &magnetMailEvent{Event: magnetMailEventSupportActivity, IdempotencyKey: "support_activity:t:0"}
	if err := deliverMagnetMail(context.Background(), ev); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if f.count() != 1 {
		t.Error("support_activity is a staff notice that Magnet holds itself; it must reach Magnet")
	}
}

func TestQuietPushIsHeld(t *testing.T) {
	useQuietWindow(t, testShabbat.StartsAt.Add(time.Hour), []quietWindow{testShabbat})
	pushFcmMessageOrHold("quiet-push", &Message{ID: 7, Text: "שלום"})
	if n, _ := rdb.ZCard(context.Background(), quietHeldPushKey).Result(); n != 1 {
		t.Fatalf("held pushes = %d, want 1", n)
	}
	quietNow = func() time.Time { return testShabbat.EndsAt.Add(time.Minute) }
	releaseHeld(context.Background())
	if n, _ := rdb.ZCard(context.Background(), quietHeldPushKey).Result(); n != 0 {
		t.Fatalf("held pushes after the window = %d, want 0", n)
	}
}

func TestQuietRefusalParsing(t *testing.T) {
	if _, q := quietRefusal(http.StatusConflict, []byte(`{"error":"in_progress"}`)); q {
		t.Error("in_progress is not a quiet refusal")
	}
	if _, q := quietRefusal(http.StatusOK, []byte(`{"error":"quiet_window","quiet_until":"2026-11-28T15:19:00Z"}`)); q {
		t.Error("only a 409 is a refusal")
	}
}
