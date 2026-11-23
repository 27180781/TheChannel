package main

import (
	"context"
	"testing"
	"time"
)

// Many streams of one person count once; a person whose streams all went
// quiet ages out; a malformed stamp is pruned rather than counted.
func TestSSEViewerCountDistinctPeople(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const slug = "sse-viewers-count"
	key := sseViewersKey(slug)
	rdb.Del(ctx, key)
	t.Cleanup(func() { rdb.Del(context.Background(), key) })

	alice := sseViewerKey("alice@example.com", "10.0.0.1")
	for i := 0; i < 12; i++ { // twelve tabs and devices
		touchSSEViewer(slug, alice)
	}
	touchSSEViewer(slug, sseViewerKey("", "10.0.0.2"))
	stale := time.Now().Add(-2 * sseViewerFreshness).Unix()
	rdb.HSet(ctx, key, "email:gone@example.com", stale, "email:junk@example.com", "x")

	if got := dbGetSSEViewerCount(ctx, slug); got != 2 {
		t.Fatalf("distinct people = %d, want 2", got)
	}
	if n := rdb.HLen(ctx, key).Val(); n != 2 {
		t.Fatalf("stale entries not pruned: %d fields left, want 2", n)
	}
}
