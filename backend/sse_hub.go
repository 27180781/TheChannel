package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// One Redis reader per channel stream, shared by every viewer of that channel.
//
// Each viewer used to run its own blocking XREAD, which holds a pooled
// connection for the whole block and is re-issued immediately — so viewer count
// and Redis connection count were the same number, and the pool was the ceiling
// on concurrent viewers. Now the ceiling is per *channel with at least one
// viewer*, and a channel with ten thousand readers costs exactly one connection.
//
// The hub reads from the tip of the stream. A viewer that needs earlier events
// (a reconnect carrying Last-Event-ID) catches up with its own bounded XRANGE
// before joining the live feed; see sseCatchUp.

const (
	// sseSubBuffer is how many events a single slow viewer may fall behind by
	// before it is dropped. Dropping one viewer is correct: the alternative is
	// letting it block the hub and stall delivery for everybody else on the
	// channel. A dropped viewer's browser reconnects with Last-Event-ID and
	// catches up.
	sseSubBuffer = 256

	// sseHubBlock is how long the hub's read blocks before looping. It only
	// needs to be short enough to notice that the hub has been told to stop.
	sseHubBlock = 5 * time.Second

	// sseCatchUpLimit bounds a reconnecting viewer's replay. The stream itself
	// only retains ~1000 entries (publishEvent), so this cannot silently skip
	// events a client could otherwise have had.
	sseCatchUpLimit = 1000
)

// sseEvent is one stream entry, broadcast verbatim. Per-viewer transformation
// (author masking for sub-writer viewers) happens at the subscriber, because it
// depends on who is watching rather than on the event.
type sseEvent struct {
	id   string
	data string
}

type sseSubscriber struct {
	ch chan sseEvent
	// closed guards against a double close when a slow subscriber is dropped by
	// the hub and then unsubscribes itself.
	once sync.Once
}

func (s *sseSubscriber) close() {
	s.once.Do(func() { close(s.ch) })
}

type sseHub struct {
	streamKey string

	mu   sync.Mutex
	subs map[*sseSubscriber]struct{}
	// stop ends the reader goroutine once the last subscriber leaves.
	stop context.CancelFunc
}

var (
	sseHubsMu sync.Mutex
	sseHubs   = map[string]*sseHub{}
)

// sseSubscribe joins the stream's hub, starting it if this is the first viewer.
// The returned function must be called when the viewer goes away.
func sseSubscribe(streamKey string) (*sseSubscriber, func()) {
	sub := &sseSubscriber{ch: make(chan sseEvent, sseSubBuffer)}

	sseHubsMu.Lock()
	hub, ok := sseHubs[streamKey]
	if !ok {
		ctx, cancel := context.WithCancel(context.Background())
		hub = &sseHub{
			streamKey: streamKey,
			subs:      map[*sseSubscriber]struct{}{},
			stop:      cancel,
		}
		sseHubs[streamKey] = hub
		go hub.run(ctx)
	}
	sseHubsMu.Unlock()

	hub.mu.Lock()
	hub.subs[sub] = struct{}{}
	hub.mu.Unlock()

	return sub, func() { sseUnsubscribe(hub, sub) }
}

func sseUnsubscribe(hub *sseHub, sub *sseSubscriber) {
	hub.mu.Lock()
	delete(hub.subs, sub)
	remaining := len(hub.subs)
	hub.mu.Unlock()
	sub.close()

	if remaining > 0 {
		return
	}

	// Last viewer of this channel left: retire the hub and its connection.
	// Re-checked under the registry lock, because a new subscriber may have
	// arrived in the meantime and must not be left attached to a stopped hub.
	sseHubsMu.Lock()
	defer sseHubsMu.Unlock()
	if sseHubs[hub.streamKey] != hub {
		return
	}
	hub.mu.Lock()
	stillEmpty := len(hub.subs) == 0
	hub.mu.Unlock()
	if stillEmpty {
		delete(sseHubs, hub.streamKey)
		hub.stop()
	}
}

// run is the single reader for one stream. It starts at the tip: viewers that
// need earlier events replay them themselves before joining.
func (h *sseHub) run(ctx context.Context) {
	lastID := "$"
	failures := 0

	for {
		if ctx.Err() != nil {
			h.shutdown()
			return
		}

		streams, err := rdbEvents.XRead(ctx, &redis.XReadArgs{
			Streams: []string{h.streamKey, lastID},
			Count:   100,
			Block:   sseHubBlock,
		}).Result()

		if ctx.Err() != nil {
			h.shutdown()
			return
		}
		if err != nil {
			// redis.Nil is an ordinary "nothing arrived before the block
			// elapsed". Anything else is retried, but not for ever: a stream
			// that keeps failing must not spin.
			if err != redis.Nil {
				failures++
				if failures > maxStreamReadFailures {
					log.Printf("SSE hub %s: giving up after %d failed reads: %v\n", h.streamKey, failures, err)
					h.shutdown()
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
			continue
		}
		failures = 0

		for _, stream := range streams {
			for _, msg := range stream.Messages {
				lastID = msg.ID
				data, _ := msg.Values["data"].(string)
				h.broadcast(sseEvent{id: msg.ID, data: data})
			}
		}
	}
}

// broadcast delivers to every subscriber without ever blocking on one of them.
func (h *sseHub) broadcast(ev sseEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		select {
		case sub.ch <- ev:
		default:
			// This viewer is not draining fast enough. Drop it rather than
			// stalling the channel for everyone; its browser will reconnect and
			// resume from Last-Event-ID.
			delete(h.subs, sub)
			sub.close()
			log.Printf("SSE hub %s: dropped a subscriber that fell more than %d events behind\n",
				h.streamKey, sseSubBuffer)
		}
	}
}

// shutdown releases every subscriber still attached when the reader stops, so
// their handlers return instead of waiting on a channel nothing will write to.
func (h *sseHub) shutdown() {
	sseHubsMu.Lock()
	if sseHubs[h.streamKey] == h {
		delete(sseHubs, h.streamKey)
	}
	sseHubsMu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		delete(h.subs, sub)
		sub.close()
	}
}

// sseCatchUp replays entries after lastID for a reconnecting viewer.
//
// The hub reads from the tip, so anything published between the viewer's last
// received event and the moment it subscribed exists only in the stream. The
// caller subscribes first and replays second, so an event arriving during the
// replay is buffered rather than lost; sending is then deduplicated on id.
func sseCatchUp(ctx context.Context, streamKey, lastID string) []sseEvent {
	if lastID == "" || lastID == "$" {
		return nil
	}
	// "(" makes the range exclusive, so the client's last event is not resent.
	msgs, err := rdbEvents.XRangeN(ctx, streamKey, "("+lastID, "+", sseCatchUpLimit).Result()
	if err != nil {
		if err != redis.Nil {
			log.Printf("SSE catch-up on %s from %s failed: %v\n", streamKey, lastID, err)
		}
		return nil
	}
	out := make([]sseEvent, 0, len(msgs))
	for _, m := range msgs {
		data, _ := m.Values["data"].(string)
		out = append(out, sseEvent{id: m.ID, data: data})
	}
	return out
}

// sseHubCount reports how many hubs are running, i.e. how many Redis
// connections the event system is holding. Used by tests.
func sseHubCount() int {
	sseHubsMu.Lock()
	defer sseHubsMu.Unlock()
	return len(sseHubs)
}
