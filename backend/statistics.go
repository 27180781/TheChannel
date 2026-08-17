package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The live SSE connection count is shared through Redis so that the statistics
// screen is correct on multi-instance deployments (the whole point of the
// Redis-Streams SSE layer). Each instance publishes its own local count as a
// timestamped field of one hash per channel; readers sum the fields that are
// still fresh, so a crashed instance's contribution ages out instead of
// inflating the total forever.

// sseInstanceID identifies this process's field in the per-channel hash.
var sseInstanceID = generatedRandomID(8)

// channelSSEConnections holds this instance's local per-channel counters.
var channelSSEConnections sync.Map // slug -> *atomic.Int64

// sseConnFreshness is how long a published per-instance count stays credible
// without a refresh; the refresher below re-publishes well within it.
const sseConnFreshness = 90 * time.Second

func sseConnectionsKey(slug string) string {
	return fmt.Sprintf("channel:%s:sse_connections", slug)
}

func getOrCreateChannelCounter(slug string) *atomic.Int64 {
	v, _ := channelSSEConnections.LoadOrStore(slug, &atomic.Int64{})
	return v.(*atomic.Int64)
}

// publishLocalSSECount writes this instance's current count for slug.
func publishLocalSSECount(ctx context.Context, slug string, count int64) {
	rdb.HSet(ctx, sseConnectionsKey(slug), sseInstanceID,
		fmt.Sprintf("%d:%d", count, time.Now().Unix()))
}

// dbGetSSEConnectionCount sums every instance's fresh published count for slug,
// pruning fields that have gone stale (crashed or scaled-down instances).
func dbGetSSEConnectionCount(ctx context.Context, slug string) int64 {
	fields, err := rdb.HGetAll(ctx, sseConnectionsKey(slug)).Result()
	if err != nil {
		return getOrCreateChannelCounter(slug).Load() // best effort: local view
	}
	var total int64
	cutoff := time.Now().Add(-sseConnFreshness).Unix()
	for field, v := range fields {
		i := strings.LastIndex(v, ":")
		if i <= 0 {
			rdb.HDel(ctx, sseConnectionsKey(slug), field)
			continue
		}
		count, cerr := strconv.ParseInt(v[:i], 10, 64)
		ts, terr := strconv.ParseInt(v[i+1:], 10, 64)
		if cerr != nil || terr != nil || ts < cutoff {
			rdb.HDel(ctx, sseConnectionsKey(slug), field)
			continue
		}
		total += count
	}
	return total
}

// The refresher keeps long-lived quiet connections visible: without it an
// instance whose count has not changed for sseConnFreshness would age out of
// every reader's sum despite still serving clients.
func init() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			channelSSEConnections.Range(func(key, value any) bool {
				slug := key.(string)
				count := value.(*atomic.Int64).Load()
				if count <= 0 {
					// Nothing connected here: withdraw our field and forget the
					// slug. A racing increment re-publishes immediately after.
					rdb.HDel(ctx, sseConnectionsKey(slug), sseInstanceID)
					channelSSEConnections.Delete(slug)
					return true
				}
				publishLocalSSECount(ctx, slug, count)
				return true
			})
			cancel()
		}
	}()
}

type PeakSSEConnections struct {
	Value     int64     `json:"value" redis:"value"`
	Timestamp time.Time `json:"timestamp" redis:"timestamp"`
}
type Statistics struct {
	Data   []int64  `json:"date"`
	Labels []string `json:"labels"`
}

func increaseCounterSSE(slug string) {
	local := getOrCreateChannelCounter(slug).Add(1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	publishLocalSSECount(ctx, slug, local)

	total := dbGetSSEConnectionCount(ctx, slug)
	if total < local {
		total = local
	}

	// dbSavePeakSSEConnections is a Lua max-write, so publishing the current
	// total unconditionally can never regress a larger recorded peak.
	go dbSavePeakSSEConnections(slug, &PeakSSEConnections{Value: total, Timestamp: time.Now()})
	go dbSaveSSEStatistics(slug, total)
}

func decreaseCounterSSE(slug string) {
	counter := getOrCreateChannelCounter(slug)
	local := counter.Add(-1)
	// A decrement that outruns its increment must not persist a negative
	// datapoint into the statistics series.
	if local < 0 {
		counter.Store(0)
		local = 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	publishLocalSSECount(ctx, slug, local)

	go dbSaveSSEStatistics(slug, dbGetSSEConnectionCount(ctx, slug))
}

func getStatistics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)

	amount, err := dbGetUsersAmount(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	s, err := dbGetSSEStatistics(ctx, slug, 1000)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// Redis is authoritative for both the live count and the peak, so every
	// replica answers the same numbers and resetStatistics sticks everywhere.
	peak, err := dbGetPeakSSEConnections(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	response := struct {
		UsersAmount           int64               `json:"usersAmount"`
		ConnectedUsersAmount  int64               `json:"connectedUsersAmount"`
		PeakSSEConnections    *PeakSSEConnections `json:"peakSSEConnections"`
		ConnectionsStatistics Statistics          `json:"connectionsStatistics"`
	}{
		UsersAmount:           amount,
		ConnectedUsersAmount:  dbGetSSEConnectionCount(ctx, slug),
		PeakSSEConnections:    peak,
		ConnectionsStatistics: *s,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// resetStatistics clears everything the statistics endpoint serves: the
// recorded peak AND the monthly connection series. The peak lives only in
// Redis (max-written, read back on demand), so deleting the key is a complete
// reset on every replica.
func resetStatistics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	channels, err := dbListChannels(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	currentYear := time.Now().Year()
	for _, ch := range channels {
		rdb.Del(ctx, fmt.Sprintf("channel:%s:peak_sse_connections", ch.Slug))
		// The monthly series keys have no index; enumerate the possible
		// month/year combinations (mirrors dbDeleteChannel).
		for year := currentYear - 10; year <= currentYear; year++ {
			for month := time.January; month <= time.December; month++ {
				rdb.Del(ctx, fmt.Sprintf("channel:%s:sse_statistics:%d:%d", ch.Slug, month, year))
			}
		}
	}

	var response Response
	response.Success = true
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
