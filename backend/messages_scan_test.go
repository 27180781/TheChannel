package main

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// seedMessages writes n messages into a channel, marking every message whose id
// satisfies deleted(id) as soft-deleted.
func seedMessages(t *testing.T, ctx context.Context, slug string, n int, deleted func(int) bool) {
	t.Helper()
	timesKey := "channel:" + slug + ":m_times"
	for i := 1; i <= n; i++ {
		key := "channel:" + slug + ":messages:" + strconv.Itoa(i)
		fields := map[string]any{
			"id":     strconv.Itoa(i),
			"text":   "message " + strconv.Itoa(i),
			"author": "someone",
			"views":  "0",
		}
		if deleted(i) {
			fields["deleted"] = "1"
		}
		if err := rdb.HSet(ctx, key, fields).Err(); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
		if err := rdb.ZAdd(ctx, timesKey, redis.Z{Score: float64(i), Member: key}).Err(); err != nil {
			t.Fatalf("index message %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for i := 1; i <= n; i++ {
			rdb.Del(cctx, "channel:"+slug+":messages:"+strconv.Itoa(i))
		}
		rdb.Del(cctx, timesKey)
	})
}

// Ordinary paging must be unchanged by the scan bound: a page of live messages
// still comes back full, newest first.
func TestMessageRangeReturnsFullPageOfLiveMessages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "scan-live"
	seedMessages(t, ctx, slug, 60, func(int) bool { return false })

	msgs, err := funcGetMessageRange(ctx, slug, 0, 20, false, false, "desc")
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(msgs) != 20 {
		t.Fatalf("got %d messages, want a full page of 20", len(msgs))
	}
	// desc: newest first.
	if msgs[0].ID != 60 {
		t.Errorf("first message id = %d, want 60 (newest first)", msgs[0].ID)
	}
	if msgs[19].ID != 41 {
		t.Errorf("last message id = %d, want 41", msgs[19].ID)
	}
}

// Soft-deleted messages are skipped for a non-admin, and the scan still reaches
// past a short run of them to fill the page.
func TestMessageRangeSkipsDeletedAndStillFillsPage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "scan-mixed"
	// Every other message deleted: filling a page of 10 needs ~20 entries.
	seedMessages(t, ctx, slug, 100, func(i int) bool { return i%2 == 0 })

	msgs, err := funcGetMessageRange(ctx, slug, 0, 10, false, false, "desc")
	if err != nil {
		t.Fatalf("range: %v", err)
	}
	if len(msgs) != 10 {
		t.Fatalf("got %d messages, want 10: the scan should look past tombstones", len(msgs))
	}
	for _, m := range msgs {
		if m.ID%2 == 0 {
			t.Errorf("message %d is soft-deleted and must not be returned to a non-admin", m.ID)
		}
	}
}

// The defect: a long run of tombstones meant #messages never grew, so
// batch_size stayed at the full page size for every one of 20 rounds — up to
// 2000 HGETALLs in a single atomic script, blocking every other tenant.
//
// The scan is now bounded by entries examined. This asserts the request still
// returns promptly and correctly over a large tombstone field rather than doing
// unbounded work.
func TestMessageRangeBoundsWorkOverLongTombstoneRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "scan-tombstones"
	// 900 consecutive deleted messages, then 40 live ones underneath.
	const total = 940
	seedMessages(t, ctx, slug, total, func(i int) bool { return i > 40 })

	start := time.Now()
	msgs, err := funcGetMessageRange(ctx, slug, 0, 100, false, false, "desc")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("range: %v", err)
	}

	t.Logf("scanned a %d-entry tombstone run in %v, returned %d messages", total-40, elapsed, len(msgs))

	// Whatever it returns must be correct: never a deleted message.
	for _, m := range msgs {
		if m.ID > 40 {
			t.Errorf("returned soft-deleted message %d", m.ID)
		}
	}
	// The point of the bound is that one request cannot monopolise Redis.
	if elapsed > 2*time.Second {
		t.Errorf("request took %v over a tombstone run: the scan is not bounded", elapsed)
	}
}

// Asking for a page when nothing is stored must be empty, not an error.
func TestMessageRangeEmptyChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msgs, err := funcGetMessageRange(ctx, "scan-empty-channel", 0, 20, false, false, "desc")
	if err != nil {
		t.Fatalf("range on empty channel: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("got %d messages from an empty channel", len(msgs))
	}
}
