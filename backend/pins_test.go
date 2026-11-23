package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func pinRequest(slug string, id int, pinned bool) *httptest.ResponseRecorder {
	b, _ := json.Marshal(map[string]any{"id": id, "pinned": pinned})
	req := httptest.NewRequest(http.MethodPost, "/api/channel/"+slug+"/admin/pin-message", bytes.NewReader(b))
	req = req.WithContext(context.WithValue(req.Context(), channelCtxKey, &ChannelData{Slug: slug}))
	w := httptest.NewRecorder()
	setPinnedMessage(w, req)
	return w
}

func TestPinnedMessageFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const slug = "pins-flow"
	t.Cleanup(func() {
		cctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		for i := 1; i <= 3; i++ {
			rdb.Del(cctx, "channel:"+slug+":messages:"+strconv.Itoa(i))
		}
		rdb.Del(cctx, "channel:"+slug+":m_times", "channel:"+slug+":events", "channel:"+slug+":last_modified",
			"channel:"+slug+":settings", channelPinnedKey(slug))
	})

	base := time.Now().Add(-time.Hour)
	texts := []string{
		"**כותרת** חשובה\n[image-embedded#800x600](/api/channel/pins-flow/files/abc)",
		"שנייה",
		"שלישית",
	}
	for i, txt := range texts {
		m := Message{ID: i + 1, Type: "md", Text: txt, Timestamp: base.Add(time.Duration(i) * time.Minute)}
		if err := setMessage(ctx, slug, &m, false); err != nil {
			t.Fatal(err)
		}
	}

	if p, err := dbGetPinned(ctx, slug); err != nil || p != nil {
		t.Fatalf("nothing pinned yet: got %+v, %v", p, err)
	}

	if w := pinRequest(slug, 1, true); w.Code != http.StatusOK {
		t.Fatalf("pin: %d %s", w.Code, w.Body.String())
	}
	p, err := dbGetPinned(ctx, slug)
	if err != nil || p == nil || p.ID != 1 {
		t.Fatalf("pinned = %+v, %v", p, err)
	}
	if p.Preview != "כותרת חשובה" || p.Image != "/api/channel/pins-flow/files/abc" {
		t.Errorf("preview = %q image = %q", p.Preview, p.Image)
	}

	// Unknown post: refused, pin untouched.
	if w := pinRequest(slug, 99, true); w.Code != http.StatusNotFound {
		t.Errorf("pin of a missing post = %d, want 404", w.Code)
	}
	// Unpinning another post leaves the current pin alone.
	if w := pinRequest(slug, 2, false); w.Code != http.StatusOK {
		t.Fatalf("unpin other: %d", w.Code)
	}
	if p, _ := dbGetPinned(ctx, slug); p == nil || p.ID != 1 {
		t.Errorf("unpinning post 2 removed the pin of post 1: %+v", p)
	}
	// Pinning another replaces it.
	pinRequest(slug, 2, true)
	if p, _ := dbGetPinned(ctx, slug); p == nil || p.ID != 2 {
		t.Errorf("pin was not replaced: %+v", p)
	}

	// Deleting a post that is not pinned keeps the pin; deleting the pinned one drops it.
	if err := funcDeleteMessage(ctx, slug, "3", ""); err != nil {
		t.Fatal(err)
	}
	unpinIfPinned(ctx, slug, "3")
	if p, _ := dbGetPinned(ctx, slug); p == nil || p.ID != 2 {
		t.Errorf("deleting post 3 removed the pin: %+v", p)
	}
	if w := pinRequest(slug, 3, true); w.Code != http.StatusConflict {
		t.Errorf("pin of a deleted post = %d, want 409", w.Code)
	}
	if err := funcDeleteMessage(ctx, slug, "2", ""); err != nil {
		t.Fatal(err)
	}
	unpinIfPinned(ctx, slug, "2")
	if n, _ := rdb.Exists(ctx, channelPinnedKey(slug)).Result(); n != 0 {
		t.Errorf("pin of a deleted post survived")
	}

	if w := pinRequest(slug, 1, true); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if w := pinRequest(slug, 1, false); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if p, _ := dbGetPinned(ctx, slug); p != nil {
		t.Errorf("unpin left %+v", p)
	}
}
