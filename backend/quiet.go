package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Shabbat and Yom Tov (the owner's rule): from an hour before candle lighting
// to an hour after havdalah nothing leaves this system — no reminder mails,
// no push, no scheduled posts, no mail through Magnet. Nothing is dropped
// either: what falls inside the window is held and goes out when it closes.
//
// The calendar is not computed here. Magnet keeps it in one table
// (public.quiet_windows) and channels-mail hands out the coming windows
// ({"quiet_windows": true}); this side caches them in memory and in Redis, so
// a restart during Shabbat still knows it is Shabbat. Magnet also refuses a
// mail inside a window (409 quiet_window), which is the second fence.

const (
	quietWindowsKey   = "quiet:windows"
	quietHeldMailKey  = "quiet:held:mail"
	quietHeldPushKey  = "quiet:held:push"
	quietRefreshEvery = 6 * time.Hour
	quietReleaseEvery = time.Minute
)

type quietWindow struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Label    string    `json:"label"`
}

var (
	quietMu      sync.RWMutex
	quietWindows []quietWindow
	// quietNow is the clock, a var so tests can move it.
	quietNow   = time.Now
	quietStart sync.Once
)

// setQuietWindows replaces the cached calendar, sorted by start.
func setQuietWindows(ws []quietWindow) {
	cp := append([]quietWindow(nil), ws...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].StartsAt.Before(cp[j].StartsAt) })
	quietMu.Lock()
	quietWindows = cp
	quietMu.Unlock()
}

// quietUntil reports when the window holding t closes, and whether t is in one.
func quietUntil(t time.Time) (time.Time, bool) {
	quietMu.RLock()
	defer quietMu.RUnlock()
	for _, w := range quietWindows {
		if !t.Before(w.StartsAt) && t.Before(w.EndsAt) {
			return w.EndsAt, true
		}
	}
	return time.Time{}, false
}

func isQuietNow() bool {
	_, q := quietUntil(quietNow())
	return q
}

// refreshQuietWindows asks Magnet for the calendar and keeps it. On failure
// the last calendar stays (memory, then Redis), so a Magnet outage during
// Shabbat does not open the gate.
func refreshQuietWindows(ctx context.Context) error {
	ws, err := fetchQuietWindows(ctx)
	if err != nil {
		return err
	}
	setQuietWindows(ws)
	if b, err := json.Marshal(ws); err == nil && rdb != nil {
		rdb.Set(ctx, quietWindowsKey, b, 0)
	}
	return nil
}

func loadCachedQuietWindows(ctx context.Context) {
	if rdb == nil {
		return
	}
	b, err := rdb.Get(ctx, quietWindowsKey).Bytes()
	if err != nil {
		return
	}
	var ws []quietWindow
	if json.Unmarshal(b, &ws) == nil {
		setQuietWindows(ws)
	}
}

func fetchQuietWindows(ctx context.Context) ([]quietWindow, error) {
	if !magnetMailEnabled() {
		return nil, errors.New("magnet mail is not configured")
	}
	status, body, err := postMagnet(ctx, []byte(`{"quiet_windows":true}`))
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, errors.New("quiet windows: unexpected status")
	}
	var out struct {
		OK      bool          `json:"ok"`
		Windows []quietWindow `json:"windows"`
	}
	if err := json.Unmarshal(body, &out); err != nil || !out.OK {
		return nil, errors.New("quiet windows: unreadable answer")
	}
	return out.Windows, nil
}

// quietRefusal reads Magnet's 409 quiet_window answer: when the window ends.
func quietRefusal(status int, body []byte) (time.Time, bool) {
	if status != http.StatusConflict {
		return time.Time{}, false
	}
	var r struct {
		Error      string    `json:"error"`
		QuietUntil time.Time `json:"quiet_until"`
	}
	if json.Unmarshal(body, &r) != nil || r.Error != "quiet_window" || r.QuietUntil.IsZero() {
		return time.Time{}, false
	}
	return r.QuietUntil, true
}

// holdMagnetMail keeps a mail for the window's end. The payload is the same
// body with the same idempotency key, so a mail held twice still goes once.
func holdMagnetMail(ctx context.Context, payload []byte, until time.Time) error {
	if rdb == nil {
		return errors.New("no redis")
	}
	return rdb.ZAdd(ctx, quietHeldMailKey, redis.Z{Score: float64(until.Unix()), Member: string(payload)}).Err()
}

type heldPush struct {
	Slug    string  `json:"slug"`
	Message Message `json:"message"`
}

// pushFcmMessageOrHold is pushFcmMessage outside the window; inside it the
// push waits for the window's end.
func pushFcmMessageOrHold(slug string, m *Message) {
	until, quiet := quietUntil(quietNow())
	if !quiet {
		pushFcmMessage(slug, m)
		return
	}
	b, err := json.Marshal(heldPush{Slug: slug, Message: *m})
	if err == nil && rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = rdb.ZAdd(ctx, quietHeldPushKey, redis.Z{Score: float64(until.Unix()), Member: string(b)}).Err()
	}
	if err != nil {
		log.Printf("quiet: push for %s could not be held: %v", slug, err)
	}
}

// releaseHeld sends what was held for windows that have closed. A member is
// removed before it is sent (ZRem decides which instance owns it), so two
// instances never send the same one.
func releaseHeld(ctx context.Context) {
	if rdb == nil || isQuietNow() {
		return
	}
	max := float64(quietNow().Unix())
	for _, key := range []string{quietHeldMailKey, quietHeldPushKey} {
		items, err := rdb.ZRangeByScore(ctx, key, &redis.ZRangeBy{Min: "-inf", Max: formatScore(max), Count: 200}).Result()
		if err != nil {
			continue
		}
		for _, it := range items {
			if n, err := rdb.ZRem(ctx, key, it).Result(); err != nil || n == 0 {
				continue
			}
			if key == quietHeldMailKey {
				var ev magnetMailEvent
				if json.Unmarshal([]byte(it), &ev) != nil {
					continue
				}
				evCopy := ev
				dispatchMagnetMail(func(context.Context) (*magnetMailEvent, bool) { return &evCopy, true })
				continue
			}
			var hp heldPush
			if json.Unmarshal([]byte(it), &hp) != nil {
				continue
			}
			go pushFcmMessage(hp.Slug, &hp.Message)
		}
	}
}

func formatScore(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// startQuietWindows loads the calendar and runs the releaser. Called from main.
func startQuietWindows() {
	quietStart.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		loadCachedQuietWindows(ctx)
		cancel()
		go func() {
			var lastRefresh time.Time
			for {
				func() {
					defer func() {
						if p := recover(); p != nil {
							log.Printf("quiet: panic: %v", p)
						}
					}()
					ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					if time.Since(lastRefresh) >= quietRefreshEvery && magnetMailEnabled() {
						if err := refreshQuietWindows(ctx); err != nil {
							log.Printf("quiet: calendar refresh failed, keeping the cached one: %v", err)
						} else {
							lastRefresh = time.Now()
						}
					}
					releaseHeld(ctx)
				}()
				time.Sleep(quietReleaseEvery)
			}
		}()
	})
}

// postMagnet posts to channels-mail and returns the status and a bounded body.
func postMagnet(ctx context.Context, payload []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, magnetMailURL, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, errors.New("request build failed — check MAGNET_MAIL_URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-channels-secret", magnetMailSecret)
	resp, err := magnetMailClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, body, nil
}
