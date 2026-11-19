package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// The super admin's views chart (channel_views.go). Real Redis, like the
// other statistics tests.

func cleanupViews(t *testing.T, slugs ...string) {
	t.Helper()
	ctx := testCtx(t)
	t.Cleanup(func() {
		for _, s := range slugs {
			_ = dbDeleteChannel(ctx, s)
		}
	})
}

func TestFeedVisitIsOnlyTheFirstPage(t *testing.T) {
	cases := map[string]bool{
		"/messages?offset=0&limit=20&direction=desc":         true,
		"/messages?limit=20&direction=desc":                  true,
		"/messages?offset=40&limit=20&direction=desc":        false,
		"/messages?offset=0&limit=20&direction=asc":          false,
		"/messages?offset=0&limit=20&direction=desc&tag=abc": false,
	}
	for u, want := range cases {
		r := httptest.NewRequest(http.MethodGet, u, nil)
		if got := isFeedVisit(r); got != want {
			t.Errorf("%s: %v, want %v", u, got, want)
		}
	}
}

func TestChannelViewsCountsVisitsAndRanks(t *testing.T) {
	ctx := testCtx(t)
	a, b := "views-test-a", "views-test-b"
	seedChannel(t, ctx, a, Settings{})
	seedChannel(t, ctx, b, Settings{})
	cleanupViews(t, a, b)

	recordChannelVisit(a, "ip:1.1.1.1")
	recordChannelVisit(a, "ip:1.1.1.1")
	recordChannelVisit(a, "ip:2.2.2.2")
	recordChannelVisit(b, "ip:3.3.3.3")
	addViewsToMessages(ctx, b, true, []Message{{ID: 1}, {ID: 2}, {ID: 3}})
	t.Cleanup(func() {
		for i := 1; i <= 3; i++ {
			rdb.Del(ctx, fmt.Sprintf("channel:%s:messages:%d", b, i))
		}
	})

	today := viewsDay(time.Now())
	ans, err := collectChannelsViews(ctx, []*ChannelData{{Slug: b, Name: b}, {Slug: a, Name: a}}, []string{today})
	if err != nil {
		t.Fatal(err)
	}
	if len(ans.Channels) != 2 || ans.Channels[0].Slug != a {
		t.Fatalf("ranking: %+v", ans.Channels)
	}
	ra, rb := ans.Channels[0], ans.Channels[1]
	if ra.Visits != 3 || ra.UniqueViewers != 2 || ra.Daily[0] != 3 {
		t.Fatalf("channel a: %+v", ra)
	}
	if rb.Visits != 1 || rb.MessageViews != 3 {
		t.Fatalf("channel b: %+v", rb)
	}
}

func TestChannelViewsBackfillAddsPastViewsOnce(t *testing.T) {
	ctx := testCtx(t)
	slug := "views-test-backfill"
	seedChannel(t, ctx, slug, Settings{})
	cleanupViews(t, slug)
	for id, v := range map[int]int{1: 5, 2: 7} {
		key := fmt.Sprintf("channel:%s:messages:%d", slug, id)
		rdb.HSet(ctx, key, "id", id, "views", v)
		rdb.ZAdd(ctx, fmt.Sprintf("channel:%s:m_times", slug), redis.Z{Score: float64(id), Member: key})
	}
	rdb.SRem(ctx, messageViewsBackfilledKey, slug)

	backfillMessageViews(ctx)
	backfillMessageViews(ctx)

	got, _ := rdb.Get(ctx, messageViewsKey(slug)).Int64()
	if got != 12 {
		t.Fatalf("message views %d, want 12 (summed once)", got)
	}
}

func TestChannelViewsRouteAnswersJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	getChannelsViews(rec, httptest.NewRequest(http.MethodGet, "/views?days=500", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var ans channelViewsAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatal(err)
	}
	if len(ans.Days) != viewsMaxDays {
		t.Fatalf("days %d, want capped at %d", len(ans.Days), viewsMaxDays)
	}
}

func TestDeletedChannelLeavesNoViews(t *testing.T) {
	ctx := testCtx(t)
	slug := "views-test-deleted"
	seedChannel(t, ctx, slug, Settings{})
	recordChannelVisit(slug, "ip:9.9.9.9")
	addViewsToMessages(ctx, slug, true, []Message{{ID: 1}})
	// The post hash here was never indexed, so the delete cannot find it.
	t.Cleanup(func() { rdb.Del(ctx, fmt.Sprintf("channel:%s:messages:1", slug)) })
	if err := dbDeleteChannel(ctx, slug); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{visitsDailyKey(slug), messageViewsKey(slug), viewersDayKey(slug, viewsDay(time.Now()))} {
		if n, _ := rdb.Exists(ctx, k).Result(); n != 0 {
			t.Fatalf("%s survived the delete", k)
		}
	}
}
