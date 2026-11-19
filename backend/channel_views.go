package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Per-channel audience, for the super admin's "which channels are worth the
// most" chart in Magnet (GET /api/integrations/magnet/admin/views).
//
// Before this the platform kept only one number per post (the "views" field
// of the message hash, and only where features.countViews is on), with no
// date: nothing could answer "how many people read channel X last week". So
// every channel now keeps, whatever its countViews setting:
//
//   channel:<slug>:visits_daily      HASH  date -> visits that day
//   channel:<slug>:viewers:<date>    HLL   distinct visitors that day (TTL)
//   channel:<slug>:message_views     STRING running total of post views
//
// A visit is one opening of the feed: the first page of a fresh load
// (offset 0, newest first, no tag filter). Scrolling back, catching up after
// a reconnect, and filtering by tag fetch more pages of the same visit and are
// not counted again. Staff (writer and up) and crawlers are not audience and
// are left out. The record is taken before the ETag short-circuit, so a
// returning reader whose page comes back 304 is still a visit.
//
// Distinct visitors are a HyperLogLog per day: 12KB at most per channel-day,
// about 0.8% error, and no email or IP is stored in a form that can be read
// back. A range's count is PFCOUNT over its days, which is the union.
//
// message_views is the platform-wide meaning of the existing per-post counter,
// summed per channel so the chart does not have to read every post. It is
// incremented next to the per-post counter, and channels that already had
// counted views get their past total once (backfillMessageViews).

// viewersTTL retires a day's distinct-visitor sketch. The chart offers at
// most viewsMaxDays back, so anything older is never read.
const viewersTTL = 120 * 24 * time.Hour

// viewsMaxDays bounds one chart request: a PFCOUNT over N daily sketches per
// channel is the costly part of the answer.
const viewsMaxDays = 90

func visitsDailyKey(slug string) string { return fmt.Sprintf("channel:%s:visits_daily", slug) }
func viewersDayKey(slug, day string) string {
	return fmt.Sprintf("channel:%s:viewers:%s", slug, day)
}
func messageViewsKey(slug string) string { return fmt.Sprintf("channel:%s:message_views", slug) }

// viewsDay is the calendar day of t as the chart shows it: Israel time, so a
// visit at 01:00 on a Friday counts on Friday and not on Thursday (UTC).
func viewsDay(t time.Time) string { return t.In(israelTime).Format("2006-01-02") }

// isFeedVisit reports whether a /messages request opens the feed (see above).
func isFeedVisit(r *http.Request) bool {
	q := r.URL.Query()
	off := q.Get("offset")
	return (off == "" || off == "0") && q.Get("direction") == "desc" && q.Get("tag") == ""
}

// recordChannelVisit counts one visit to slug. Best effort and off the
// request's own deadline: a failed write costs a datapoint, never a page.
func recordChannelVisit(slug, visitor string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	day := viewsDay(time.Now())
	pipe := rdb.Pipeline()
	visits := pipe.HIncrBy(ctx, visitsDailyKey(slug), day, 1)
	var pf *redis.IntCmd
	if !hllUnsupported.Load() {
		pf = pipe.PFAdd(ctx, viewersDayKey(slug, day), visitor)
		pipe.Expire(ctx, viewersDayKey(slug, day), viewersTTL)
	}
	_, _ = pipe.Exec(ctx)
	if err := visits.Err(); err != nil {
		log.Printf("recordChannelVisit: %s: %v\n", slug, err)
	}
	if pf != nil && isUnknownCommand(pf.Err()) {
		// An older Kvrocks without HyperLogLog: stop asking, once, rather
		// than logging a failure on every visit. Visits are still counted.
		if hllUnsupported.CompareAndSwap(false, true) {
			log.Printf("recordChannelVisit: distinct visitors off, the database has no PFADD: %v\n", pf.Err())
		}
	}
}

// hllUnsupported is set when the database answers PFADD/PFCOUNT as an unknown
// command (Kvrocks before 2.10). The chart then shows visits without
// distinct visitors instead of failing.
var hllUnsupported atomic.Bool

func isUnknownCommand(err error) bool {
	return err != nil && err != redis.Nil && strings.Contains(strings.ToLower(err.Error()), "unknown command")
}

// viewsKeysFor lists every audience key a channel could hold, for channel
// deletion. The daily sketches have no index; their days are enumerated over
// the TTL window, as sseStatisticsKeysFor does for its months.
func viewsKeysFor(slug string) []string {
	keys := []string{visitsDailyKey(slug), messageViewsKey(slug)}
	now := time.Now()
	for d := 0; d <= int(viewersTTL/(24*time.Hour))+1; d++ {
		keys = append(keys, viewersDayKey(slug, viewsDay(now.AddDate(0, 0, -d))))
	}
	return keys
}

type channelViewsRow struct {
	Slug    string `json:"slug"`
	Name    string `json:"name"`
	LogoUrl string `json:"logoUrl"`
	// Visits and UniqueViewers cover the requested days. UniqueViewers is -1
	// when the database cannot count them (see hllUnsupported).
	Visits        int64 `json:"visits"`
	UniqueViewers int64 `json:"uniqueViewers"`
	// MessageViews is the channel's lifetime post views (countViews channels).
	MessageViews int64 `json:"messageViews"`
	Registered   int64 `json:"registered"`
	PeakOnline   int64 `json:"peakOnline"`
	CountViews   bool  `json:"countViews"`
	Disabled     bool  `json:"disabled"`
	// Daily is visits per day, aligned with the answer's Days.
	Daily []int64 `json:"daily"`
}

type channelViewsAnswer struct {
	Days     []string          `json:"days"`
	Channels []channelViewsRow `json:"channels"`
	// Since is the first day any channel recorded a visit: the chart says
	// that before it there is no data, rather than showing zeros as real.
	Since string `json:"since,omitempty"`
}

// viewsTrackingSinceKey holds the first day visits were recorded on this
// platform (set once, NX).
const viewsTrackingSinceKey = "views:tracking_since"

// channelViewsBatch bounds one pipeline: each channel costs one HMGET, one
// PFCOUNT over up to viewsMaxDays keys and three small reads.
const channelViewsBatch = 100

// GET /views?days=N (super admin, Magnet relay).
func getChannelsViews(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days < 1 {
		days = 30
	}
	if days > viewsMaxDays {
		days = viewsMaxDays
	}

	channels, err := dbListChannels(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	dayList := make([]string, days)
	for i := 0; i < days; i++ {
		dayList[i] = viewsDay(now.AddDate(0, 0, -(days - 1 - i)))
	}

	ans, err := collectChannelsViews(ctx, channels, dayList)
	if err != nil {
		log.Printf("getChannelsViews: %v\n", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(ans)
}

func collectChannelsViews(ctx context.Context, channels []*ChannelData, dayList []string) (*channelViewsAnswer, error) {
	ans := &channelViewsAnswer{Days: dayList, Channels: make([]channelViewsRow, 0, len(channels))}
	if since, err := rdb.Get(ctx, viewsTrackingSinceKey).Result(); err == nil {
		ans.Since = since
	}

	for start := 0; start < len(channels); start += channelViewsBatch {
		end := min(start+channelViewsBatch, len(channels))
		batch := channels[start:end]

		pipe := rdb.Pipeline()
		visits := make([]*redis.SliceCmd, len(batch))
		uniques := make([]*redis.IntCmd, len(batch))
		msgViews := make([]*redis.StringCmd, len(batch))
		registered := make([]*redis.IntCmd, len(batch))
		peaks := make([]*redis.StringCmd, len(batch))
		for i, ch := range batch {
			visits[i] = pipe.HMGet(ctx, visitsDailyKey(ch.Slug), dayList...)
			keys := make([]string, len(dayList))
			for j, d := range dayList {
				keys[j] = viewersDayKey(ch.Slug, d)
			}
			if !hllUnsupported.Load() {
				uniques[i] = pipe.PFCount(ctx, keys...)
			}
			msgViews[i] = pipe.Get(ctx, messageViewsKey(ch.Slug))
			registered[i] = pipe.SCard(ctx, fmt.Sprintf("channel:%s:registered_emails", ch.Slug))
			peaks[i] = pipe.HGet(ctx, fmt.Sprintf("channel:%s:peak_sse_connections", ch.Slug), "value")
		}
		// Each command is read on its own below: a missing key is a
		// redis.Nil of that command only, and a database without PFCOUNT
		// must cost the distinct-visitor column, not the whole answer. A real
		// failure (connection, timeout) shows on the visits read.
		_, _ = pipe.Exec(ctx)

		for i, ch := range batch {
			row := channelViewsRow{
				Slug: ch.Slug, Name: ch.Name, LogoUrl: ch.LogoUrl,
				CountViews: ch.Features.CountViews, Disabled: ch.Features.Disabled,
				Daily: make([]int64, len(dayList)),
			}
			vals, err := visits[i].Result()
			if err != nil && err != redis.Nil {
				return nil, err
			}
			if err == nil {
				for j, v := range vals {
					if s, ok := v.(string); ok {
						n, _ := strconv.ParseInt(s, 10, 64)
						row.Daily[j] = n
						row.Visits += n
					}
				}
			}
			row.UniqueViewers = -1
			if uniques[i] != nil {
				if n, err := uniques[i].Result(); err == nil {
					row.UniqueViewers = n
				} else if isUnknownCommand(err) {
					hllUnsupported.Store(true)
				}
			}
			if s, err := msgViews[i].Result(); err == nil {
				row.MessageViews, _ = strconv.ParseInt(s, 10, 64)
			}
			row.Registered, _ = registered[i].Result()
			if s, err := peaks[i].Result(); err == nil {
				row.PeakOnline, _ = strconv.ParseInt(s, 10, 64)
			}
			ans.Channels = append(ans.Channels, row)
		}
	}

	sort.SliceStable(ans.Channels, func(a, b int) bool {
		x, y := ans.Channels[a], ans.Channels[b]
		if x.Visits != y.Visits {
			return x.Visits > y.Visits
		}
		return x.MessageViews > y.MessageViews
	})
	return ans, nil
}

// messageViewsBackfilledKey is the set of channels whose past post views were
// already added to message_views. SADD is the claim, so with several replicas
// starting together each channel is summed exactly once.
const messageViewsBackfilledKey = "views:message_views_backfilled"

// backfillMessageViews adds each channel's existing per-post view counts to
// its message_views total, once. Views recorded while one channel is being
// summed can be counted twice (in the post hash it reads and in the running
// total); that is a handful of views at the moment of the first deploy.
func backfillMessageViews(ctx context.Context) {
	rdb.SetNX(ctx, viewsTrackingSinceKey, viewsDay(time.Now()), 0)
	channels, err := dbListChannels(ctx)
	if err != nil {
		log.Printf("backfillMessageViews: list channels: %v\n", err)
		return
	}
	for _, ch := range channels {
		if ctx.Err() != nil {
			return
		}
		claimed, err := rdb.SAdd(ctx, messageViewsBackfilledKey, ch.Slug).Result()
		if err != nil || claimed == 0 {
			continue
		}
		total, err := sumMessageViews(ctx, ch.Slug)
		if err != nil {
			// Release the claim so the next start tries again.
			log.Printf("backfillMessageViews: %s: %v\n", ch.Slug, err)
			rdb.SRem(ctx, messageViewsBackfilledKey, ch.Slug)
			continue
		}
		if total > 0 {
			rdb.IncrBy(ctx, messageViewsKey(ch.Slug), total)
		}
	}
}

// sumMessageViews reads the views field of every post of slug, 500 at a time.
func sumMessageViews(ctx context.Context, slug string) (int64, error) {
	key := fmt.Sprintf("channel:%s:m_times", slug)
	var total int64
	for start := int64(0); ; start += 500 {
		members, err := rdb.ZRange(ctx, key, start, start+499).Result()
		if err != nil {
			return 0, err
		}
		if len(members) == 0 {
			return total, nil
		}
		pipe := rdb.Pipeline()
		cmds := make([]*redis.StringCmd, len(members))
		for i, m := range members {
			cmds[i] = pipe.HGet(ctx, m, "views")
		}
		if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
			return 0, err
		}
		for _, c := range cmds {
			if s, err := c.Result(); err == nil {
				n, _ := strconv.ParseInt(s, 10, 64)
				total += n
			}
		}
		if len(members) < 500 {
			return total, nil
		}
	}
}

// The backfill runs once per start, in the background, after the server has
// had time to come up: it must never delay serving.
func init() {
	go func() {
		time.Sleep(30 * time.Second)
		if rdb == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		backfillMessageViews(ctx)
	}()
}
