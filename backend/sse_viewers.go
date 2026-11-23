package main

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// "Connected now" counts live streams, and one person easily holds many: a
// tab per device, an installed app, and on phones a stream the browser
// abandoned in the background that the server keeps counting until TCP gives
// up. An owner of a channel locked to six emails who saw 36 "connected" had
// every reason to think strangers were reading. The statistics screen
// therefore also shows how many distinct people those streams belong to.
//
// A person is their signed-in email, or for a guest their network address
// (several guests behind one address count once, which is the safe side).
// Each stream stamps its person into a per-channel hash on connect and on
// every heartbeat; a person counts while some stream of theirs stamped within
// sseViewerFreshness. Nothing is removed on disconnect, because another
// stream of the same person may still be open: a departed person simply ages
// out.

// sseViewerFreshness spans a little over three heartbeats (25s), so one late
// heartbeat never drops a person who is still there.
const sseViewerFreshness = 90 * time.Second

func sseViewersKey(slug string) string {
	return fmt.Sprintf("channel:%s:sse_viewers", slug)
}

// sseViewerKey names the person behind a stream.
func sseViewerKey(email, addr string) string {
	if email != "" {
		return "email:" + email
	}
	return "ip:" + addr
}

// touchSSEViewer records that viewer has a live stream on slug right now.
func touchSSEViewer(slug, viewer string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rdb.HSet(ctx, sseViewersKey(slug), viewer, time.Now().Unix())
}

// dbGetSSEViewerCount counts the distinct people with a fresh stream on slug,
// pruning those that have aged out.
func dbGetSSEViewerCount(ctx context.Context, slug string) int64 {
	fields, err := rdb.HGetAll(ctx, sseViewersKey(slug)).Result()
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-sseViewerFreshness).Unix()
	var total int64
	var stale []string
	for field, v := range fields {
		ts, err := strconv.ParseInt(v, 10, 64)
		if err != nil || ts < cutoff {
			stale = append(stale, field)
			continue
		}
		total++
	}
	if len(stale) > 0 {
		rdb.HDel(ctx, sseViewersKey(slug), stale...)
	}
	return total
}
