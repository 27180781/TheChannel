package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// A channel's managers can pin one post to the top of the feed. The pin is a
// single message id under channelPinnedKey; pinning another post replaces it.
// Viewers do not get the post itself from here but a short preview (plain
// text and the first picture) for the bar under the header: tapping it scrolls
// the feed to the post, which the feed then loads like any quote target.

// maxPinPreviewRunes bounds the preview text; the bar shows two lines of it.
const maxPinPreviewRunes = 220

// pinUpdatedEvent tells open feeds to fetch /pinned again. It carries no
// payload: the preview is per channel, not per viewer, but keeping the stream
// entry empty means it can never leak more than /pinned would.
const pinUpdatedEvent = `{"type":"pin-updated"}`

func channelPinnedKey(slug string) string {
	return fmt.Sprintf("channel:%s:pinned", slug)
}

// PinnedMessage is the public view of the pinned post.
type PinnedMessage struct {
	ID        int       `json:"id"`
	Preview   string    `json:"preview"`
	Image     string    `json:"image,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// dbGetPinned returns the pinned post, or nil when there is none, it is gone,
// or it was deleted (a pin left behind by a delete is never shown).
func dbGetPinned(ctx context.Context, slug string) (*PinnedMessage, error) {
	id, err := rdb.Get(ctx, channelPinnedKey(slug)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fields, err := dbGetMessageFields(ctx, slug, id)
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if fields["deleted"] == "1" {
		return nil, nil
	}
	idInt, _ := strconv.Atoi(id)
	text, image := postPlainText(fields["text"])
	p := &PinnedMessage{ID: idInt, Preview: truncateRunes(text, maxPinPreviewRunes), Image: image}
	if ts, perr := time.Parse(time.RFC3339Nano, fields["timestamp"]); perr == nil {
		p.Timestamp = ts
	}
	return p, nil
}

// unpinIfScript clears the pin only while it still names ARGV[1], so a delete
// racing a fresh pin of another post cannot take that new pin down.
var unpinIfScript = redis.NewScript(`
	if redis.call('GET', KEYS[1]) == ARGV[1] then
		redis.call('DEL', KEYS[1])
		return 1
	end
	return 0
`)

// unpinIfPinned drops the pin when it names messageID, and tells open feeds.
// Called after a post is deleted.
func unpinIfPinned(ctx context.Context, slug, messageID string) {
	n, err := unpinIfScript.Run(ctx, rdb, []string{channelPinnedKey(slug)}, messageID).Int()
	if err != nil {
		log.Printf("unpin after delete (%s/%s): %v\n", slug, messageID, err)
		return
	}
	if n == 1 {
		publishEvent(ctx, slug, []byte(pinUpdatedEvent))
	}
}

func getPinnedMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	p, err := dbGetPinned(ctx, channelSlugFromCtx(r))
	if err != nil {
		log.Printf("get pinned: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"pinned": p})
}

// setPinnedMessage pins {id} or, with {pinned:false}, removes the pin (only
// when it is that post's, so two managers acting at once do not undo each
// other). Moderator level: the pin speaks for the channel.
func setPinnedMessage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	body := struct {
		ID     int  `json:"id"`
		Pinned bool `json:"pinned"`
	}{}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID <= 0 {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id := strconv.Itoa(body.ID)

	if body.Pinned {
		fields, err := dbGetMessageFields(ctx, slug, id)
		if err == redis.Nil {
			http.Error(w, "message not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		if fields["deleted"] == "1" {
			http.Error(w, "message is deleted", http.StatusConflict)
			return
		}
		if err := rdb.Set(ctx, channelPinnedKey(slug), id, 0).Err(); err != nil {
			log.Printf("pin %s/%s: %v\n", slug, id, err)
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
	} else {
		if _, err := unpinIfScript.Run(ctx, rdb, []string{channelPinnedKey(slug)}, id).Result(); err != nil {
			log.Printf("unpin %s/%s: %v\n", slug, id, err)
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
	}

	publishEvent(ctx, slug, []byte(pinUpdatedEvent))

	p, _ := dbGetPinned(ctx, slug)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "pinned": p})
}
