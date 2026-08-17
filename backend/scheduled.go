package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
)

func init() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			runScheduledMessages()
		}
	}()
}

const scheduledLockTTL = 55 * time.Second

func scheduledLockKey(slug string) string { return "scheduled:lock:" + slug }

// releaseScheduledLock deletes the claim only if it still holds our token, on a
// fresh context: the caller's context may already be expired (which would leave
// the lock lingering for its full TTL), and a plain DEL could release a lock a
// second instance has since acquired after ours expired.
var scheduledLockRelease = redis.NewScript(`
	if redis.call('get', KEYS[1]) == ARGV[1] then
		return redis.call('del', KEYS[1])
	end
	return 0
`)

func releaseScheduledLock(slug, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	scheduledLockRelease.Run(ctx, rdb, []string{scheduledLockKey(slug)}, token)
}

// runScheduledMessages only processes channels that have at least one message
// due before now, using the "scheduled:due_channels" sorted set (score = earliest
// due timestamp). This avoids querying every channel every minute.
func runScheduledMessages() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := float64(time.Now().Unix())

	// Get all channels with at least one message due by now
	slugs, err := rdb.ZRangeByScore(ctx, "scheduled:due_channels", &redis.ZRangeBy{
		Min: "0",
		Max: fmt.Sprintf("%f", now),
	}).Result()
	if err != nil || len(slugs) == 0 {
		return
	}

	for _, slug := range slugs {
		slug := slug

		// Claim the channel for this tick. Every instance runs this ticker, and
		// dispatch is a read-then-write over the pending list with no atomicity,
		// so without a claim two replicas post the same message twice. The value
		// is a unique token so only the claimant can release it, and a per-slug
		// context is used so one slow channel does not consume the outer budget
		// and starve the SetNX of every remaining due channel this tick.
		lockKey := scheduledLockKey(slug)
		token := generatedRandomID(16)
		lockCtx, lockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		ok, lerr := rdb.SetNX(lockCtx, lockKey, token, scheduledLockTTL).Result()
		lockCancel()
		if lerr != nil || !ok {
			continue
		}

		ctxGet, cancelGet := context.WithTimeout(context.Background(), 5*time.Second)
		list, err := dbGetScheduledMessages(ctxGet, slug)
		cancelGet()
		if err != nil {
			releaseScheduledLock(slug, token)
			continue
		}

		nowTime := time.Now()
		newList := make([]Message, 0)
		for _, msg := range *list {
			if !msg.Timestamp.Before(nowTime) {
				newList = append(newList, msg)
				continue
			}

			// Posted synchronously: the save below erases everything not in
			// newList, so a message that failed to post must stay pending
			// instead of vanishing with only a log line.
			postCtx, postCancel := context.WithTimeout(context.Background(), 5*time.Second)
			id, ierr := getMessageNextId(postCtx, slug)
			if ierr != nil {
				log.Printf("Failed to allocate message id for scheduled post on %s: %v\n", slug, ierr)
				postCancel()
				newList = append(newList, msg)
				continue
			}
			m := msg
			m.ID = id
			m.Timestamp = time.Now()
			m.Author = "Scheduled"
			m.AuthorId = "0"
			if serr := setMessage(postCtx, slug, &m, false); serr != nil {
				log.Printf("Failed to post scheduled message on %s: %v\n", slug, serr)
				postCancel()
				newList = append(newList, msg)
				continue
			}
			// Posting can take a while under a degraded Redis; refresh the claim
			// after each message so it cannot expire mid-run and let a second
			// replica re-read the still-unsaved list and re-post everything.
			rdb.Expire(postCtx, lockKey, scheduledLockTTL)
			postCancel()

			go SendWebhook(context.Background(), slug, "create", &m)
			go pushFcmMessage(slug, &m)
		}

		// The messages above are already posted; if this save fails they stay in
		// the pending list and repost on the next tick, so the failure must not
		// be swallowed. The save is a plain SET of the pruned list, so retrying
		// is idempotent.
		var saveErr error
		for attempt := 0; attempt < 3; attempt++ {
			ctxSave, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
			saveErr = dbSaveScheduledMessages(ctxSave, slug, &newList) // also updates the sorted set
			cancelSave()
			if saveErr == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
		if saveErr != nil {
			log.Printf("CRITICAL: failed to persist scheduled list for %s after dispatch: %v — dispatched messages will repost next tick\n", slug, saveErr)
		}

		releaseScheduledLock(slug, token)
	}
}

func getScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	messages, err := dbGetScheduledMessages(ctx, slug)
	if err != nil {
		http.Error(w, "error getting messages", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func updateScheduledMessages(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	defer r.Body.Close()

	slug := channelSlugFromCtx(r)

	var messages []Message
	if err := json.NewDecoder(r.Body).Decode(&messages); err != nil {
		http.Error(w, "error decoding messages", http.StatusBadRequest)
		return
	}

	// Any non-positive Unix timestamp (not just the exact zero time) would be
	// stored as an entry the earliest-due computation in dbSaveScheduledMessages
	// skips, and a lone such entry takes the channel out of the scheduler's due
	// set entirely — stranding it forever.
	for _, m := range messages {
		if m.Timestamp.Unix() <= 0 {
			http.Error(w, "scheduled message requires a timestamp", http.StatusBadRequest)
			return
		}
	}

	// Take the same per-channel claim the dispatcher holds: dbSaveScheduledMessages
	// is a blind SET of the whole list, so a save landing between the dispatcher's
	// read and write would be overwritten by its stale snapshot — additions vanish
	// and deleted messages get resurrected and posted.
	lockKey := scheduledLockKey(slug)
	token := generatedRandomID(16)
	acquired := false
	for attempt := 0; attempt < 10; attempt++ {
		ok, err := rdb.SetNX(ctx, lockKey, token, 10*time.Second).Result()
		if err == nil && ok {
			acquired = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !acquired {
		w.Header().Set("Retry-After", "2")
		http.Error(w, "scheduled messages busy, try again", http.StatusServiceUnavailable)
		return
	}
	defer releaseScheduledLock(slug, token)

	if err := dbSaveScheduledMessages(ctx, slug, &messages); err != nil {
		http.Error(w, "error saving messages", http.StatusInternalServerError)
		return
	}

	var res = Response{
		Success: true,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
