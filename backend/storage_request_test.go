package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func cleanupStorageRequest(t *testing.T, slug, email string) {
	t.Cleanup(func() {
		ctx := context.Background()
		if id, err := rdb.Get(ctx, storageIncreaseTicketKey(slug)).Result(); err == nil {
			rdb.Del(ctx, supportTicketKey(id))
			rdb.ZRem(ctx, supportTicketIndexKey, id)
			rdb.ZRem(ctx, supportUserIndexKey(email), id)
			rdb.ZRem(ctx, supportMatchIndexKey(email), id)
		}
		rdb.Del(ctx, storageIncreaseTicketKey(slug))
		rdb.Del(ctx, "channel:"+slug+":storage:used_bytes", "channel:"+slug+":storage:quota_bytes")
	})
}

func postStorageRequest(t *testing.T, sess Session, slug, body string) (int, map[string]any) {
	t.Helper()
	r := requestAs(t, sess, slug, http.MethodPost,
		"/api/channel/"+slug+"/admin/storage/request-increase", strings.NewReader(body), nil)
	w := httptest.NewRecorder()
	requestStorageIncrease(w, r)
	out := map[string]any{}
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// A request opens one tagged ticket with the real usage figures, and asking
// again while it waits returns the same ticket instead of a second thread.
func TestStorageIncreaseRequestOpensOneTicket(t *testing.T) {
	useTestSessionStore(t)
	const slug, email = "storage-req-one", "storage.owner@example.com"
	cleanupStorageRequest(t, slug, email)
	ctx := context.Background()
	rdb.Set(ctx, "channel:"+slug+":storage:quota_bytes", 1000*1024*1024, 0)
	rdb.Set(ctx, "channel:"+slug+":storage:used_bytes", 850*1024*1024, 0)

	sess := Session{ID: "sr1", Email: email, PublicName: "בעלים",
		ChannelRoles: map[string]ChannelRole{slug: RoleOwner}}

	code, out := postStorageRequest(t, sess, slug, `{"note":"מעלים הרבה סרטונים"}`)
	if code != http.StatusOK || out["existing"] != false {
		t.Fatalf("first request: code %d, body %v", code, out)
	}
	id, _ := out["ticketId"].(string)
	ticket, err := dbGetSupportTicket(ctx, id)
	if err != nil {
		t.Fatalf("ticket not stored: %v", err)
	}
	if ticket.Topic != supportTopicStorageIncrease || ticket.ChannelSlug != slug ||
		!ticket.Authenticated || ticket.Email != email || ticket.Status != SupportStatusOpen {
		t.Errorf("unexpected ticket: %+v", ticket)
	}
	body := ticket.Messages[0].Body
	for _, want := range []string{"850 MB", "1000 MB", "85.0%", "מעלים הרבה סרטונים"} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q: %s", want, body)
		}
	}

	if p := pendingStorageIncrease(ctx, slug); p == nil || p.TicketID != id {
		t.Errorf("pending request not reported: %+v", p)
	}

	code, out = postStorageRequest(t, sess, slug, `{}`)
	if code != http.StatusOK || out["existing"] != true || out["ticketId"] != id {
		t.Fatalf("second request should return the waiting one: code %d, body %v", code, out)
	}

	// Once the operator answers, the owner may ask again.
	if _, err := dbUpdateSupportTicket(ctx, id, func(cur *SupportTicket) error {
		appendSupportMessage(cur, "admin", operatorName, "הוגדל", SupportStatusAnswered)
		return nil
	}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if p := pendingStorageIncrease(ctx, slug); p != nil {
		t.Errorf("answered request still pending: %+v", p)
	}
	code, out = postStorageRequest(t, sess, slug, ``)
	if code != http.StatusOK || out["existing"] != false || out["ticketId"] == id {
		t.Fatalf("request after an answer should open a new ticket: code %d, body %v", code, out)
	}
	rdb.Del(ctx, supportTicketKey(id))
	rdb.ZRem(ctx, supportTicketIndexKey, id)
	rdb.ZRem(ctx, supportUserIndexKey(email), id)
}

func TestFormatStorageBytes(t *testing.T) {
	cases := map[int64]string{
		512:                          "512 B",
		2048:                         "2 KB",
		300 * 1024 * 1024:            "300 MB",
		5 * 1024 * 1024 * 1024:       "5 GB",
		(9 * 1024 * 1024 * 1024) / 2: "4.5 GB",
	}
	for in, want := range cases {
		if got := formatStorageBytes(in); got != want {
			t.Errorf("formatStorageBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
