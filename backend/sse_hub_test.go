package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func drainHubs(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for sseHubCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFor polls until cond holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// The point of the hub: viewer count and Redis connection count are no longer
// the same number. Many viewers of one channel share a single reader.
func TestSSEHubOneReaderPerChannelRegardlessOfViewers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	drainHubs(t)
	const slug = "hub-fanout"
	streamKey := "channel:" + slug + ":events"
	t.Cleanup(func() { rdb.Del(context.Background(), streamKey) })

	const viewers = 200
	subs := make([]*sseSubscriber, viewers)
	stops := make([]func(), viewers)
	for i := 0; i < viewers; i++ {
		subs[i], stops[i] = sseSubscribe(streamKey)
	}
	defer func() {
		for _, stop := range stops {
			stop()
		}
	}()

	if got := sseHubCount(); got != 1 {
		t.Fatalf("%d viewers of one channel produced %d hubs, want exactly 1", viewers, got)
	}

	// Give the reader a moment to reach its first blocking read at the tip,
	// so the event below is genuinely published after it started listening.
	time.Sleep(300 * time.Millisecond)
	publishEvent(ctx, slug, []byte(`{"type":"new-message","message":{"id":1}}`))

	// Every viewer must receive it.
	var received int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < viewers; i++ {
		wg.Add(1)
		go func(s *sseSubscriber) {
			defer wg.Done()
			select {
			case ev, ok := <-s.ch:
				if ok && ev.data != "" {
					mu.Lock()
					received++
					mu.Unlock()
				}
			case <-time.After(10 * time.Second):
			}
		}(subs[i])
	}
	wg.Wait()

	if received != viewers {
		t.Errorf("%d of %d viewers received the event", received, viewers)
	}
	t.Logf("%d viewers, %d hub(s), %d deliveries", viewers, 1, received)
}

// Distinct channels each get their own reader, and a channel's reader is
// retired once its last viewer leaves — otherwise connections leak per channel
// ever visited.
func TestSSEHubPerChannelLifecycle(t *testing.T) {
	drainHubs(t)

	keys := []string{"channel:hub-a:events", "channel:hub-b:events", "channel:hub-c:events"}
	stops := make([]func(), 0, len(keys))
	for _, k := range keys {
		_, stop := sseSubscribe(k)
		stops = append(stops, stop)
	}
	if got := sseHubCount(); got != len(keys) {
		t.Fatalf("hubs = %d, want %d (one per channel)", got, len(keys))
	}

	// A second viewer on the first channel must not create a second reader.
	_, stopExtra := sseSubscribe(keys[0])
	if got := sseHubCount(); got != len(keys) {
		t.Errorf("a second viewer created a new hub: %d, want %d", got, len(keys))
	}
	stopExtra()
	if got := sseHubCount(); got != len(keys) {
		t.Errorf("one of two viewers leaving retired the hub: %d, want %d", got, len(keys))
	}

	for _, stop := range stops {
		stop()
	}
	if !waitFor(t, 5*time.Second, func() bool { return sseHubCount() == 0 }) {
		t.Errorf("hubs still running after every viewer left: %d", sseHubCount())
	}
}

// The hub reads from the tip, so a reconnecting viewer's history has to come
// from the stream. This is what stops a reconnect from silently losing events.
func TestSSECatchUpReplaysMissedEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "hub-catchup"
	streamKey := "channel:" + slug + ":events"
	rdb.Del(ctx, streamKey)
	t.Cleanup(func() { rdb.Del(context.Background(), streamKey) })

	publishEvent(ctx, slug, []byte(`{"n":1}`))
	publishEvent(ctx, slug, []byte(`{"n":2}`))
	publishEvent(ctx, slug, []byte(`{"n":3}`))

	all, err := rdb.XRangeN(ctx, streamKey, "-", "+", 10).Result()
	if err != nil || len(all) != 3 {
		t.Fatalf("seed: got %d entries, err %v", len(all), err)
	}

	// A client that last saw the first event replays exactly the other two.
	missed := sseCatchUp(ctx, streamKey, all[0].ID)
	if len(missed) != 2 {
		t.Fatalf("replayed %d events, want 2", len(missed))
	}
	if missed[0].id != all[1].ID || missed[1].id != all[2].ID {
		t.Errorf("replayed the wrong entries: %s,%s want %s,%s",
			missed[0].id, missed[1].id, all[1].ID, all[2].ID)
	}
	// The client's own last event must not be resent.
	for _, ev := range missed {
		if ev.id == all[0].ID {
			t.Error("catch-up resent the event the client already had")
		}
	}

	// A fresh connection asks for no history.
	if got := sseCatchUp(ctx, streamKey, "$"); got != nil {
		t.Errorf("fresh connect replayed %d events, want none", len(got))
	}
}

// A viewer that stops reading must not be able to stall delivery for everyone
// else on the channel. It is dropped instead; its browser reconnects.
func TestSSEHubDropsSlowSubscriberWithoutStallingOthers(t *testing.T) {
	drainHubs(t)

	streamKey := "channel:hub-slow:events"
	hub := &sseHub{streamKey: streamKey, subs: map[*sseSubscriber]struct{}{}, stop: func() {}}

	slow := &sseSubscriber{ch: make(chan sseEvent, sseSubBuffer)}
	fast := &sseSubscriber{ch: make(chan sseEvent, sseSubBuffer)}
	hub.subs[slow] = struct{}{}
	hub.subs[fast] = struct{}{}

	// Fill the slow viewer's buffer; it never reads.
	for i := 0; i < sseSubBuffer; i++ {
		hub.broadcast(sseEvent{id: fmt.Sprintf("%d-0", i), data: "x"})
		// Keep the fast one drained so only the slow one backs up.
		<-fast.ch
	}

	// The next broadcast must not block, and must drop the slow viewer.
	done := make(chan struct{})
	go func() {
		hub.broadcast(sseEvent{id: "999-0", data: "y"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("broadcast blocked on a subscriber that stopped reading")
	}

	hub.mu.Lock()
	_, slowStillAttached := hub.subs[slow]
	_, fastStillAttached := hub.subs[fast]
	hub.mu.Unlock()

	if slowStillAttached {
		t.Error("the slow subscriber was not dropped")
	}
	if !fastStillAttached {
		t.Error("the healthy subscriber was dropped too")
	}
	// A dropped subscriber's channel is closed, so its handler returns.
	if _, ok := <-slow.ch; ok {
		// Buffered events drain first; keep draining to the close.
		for range slow.ch {
		}
	}
	// The healthy viewer still received the latest event.
	select {
	case ev := <-fast.ch:
		if ev.id != "999-0" {
			t.Errorf("healthy subscriber got %s, want 999-0", ev.id)
		}
	default:
		t.Error("healthy subscriber received nothing after the slow one was dropped")
	}
}
