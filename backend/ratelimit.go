package main

import (
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// limiterEntry pairs a limiter with its last use so idle entries can be swept:
// both maps below otherwise grow monotonically for the process lifetime (one
// entry per client IP on an unauthenticated endpoint is attacker-controlled
// once RealIP is in play).
type limiterEntry struct {
	l        *rate.Limiter
	lastSeen atomic.Int64
}

// limiterIdleTTL is well past every refill window below (the slowest is
// 60s*3), so a swept-and-recreated limiter starting with a full burst changes
// nothing a legitimate client would notice.
const limiterIdleTTL = 10 * time.Minute

// allowOrRetryAfter takes one token if one is free right now. When the bucket is
// empty it answers 429 with a Retry-After saying exactly how long to wait, and
// — crucially — consumes nothing, so a refused request costs the caller no quota.
//
// Callers must place this immediately before the work being rationed, not at the
// top of the handler: spending a token on a request that then fails validation
// makes a user pay full quota for a typo, which on the hourly creation limiter
// meant three mistakes locked the account out for twenty minutes.
func allowOrRetryAfter(w http.ResponseWriter, l *rate.Limiter, msg string) bool {
	res := l.Reserve()
	if !res.OK() {
		http.Error(w, msg, http.StatusTooManyRequests)
		return false
	}
	if d := res.Delay(); d > 0 {
		res.Cancel()
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
		http.Error(w, msg, http.StatusTooManyRequests)
		return false
	}
	return true
}

func init() {
	go func() {
		ticker := time.NewTicker(limiterIdleTTL)
		defer ticker.Stop()
		for range ticker.C {
			cutoff := time.Now().Add(-limiterIdleTTL).Unix()
			sweep := func(m *sync.Map) {
				m.Range(func(key, value any) bool {
					if e, ok := value.(*limiterEntry); ok && e.lastSeen.Load() < cutoff {
						m.Delete(key)
					}
					return true
				})
			}
			sweep(&uploadLimiters)
			sweep(&channelRequestLimiters)
			sweep(&channelCreateLimiters)
			sweep(&slugCheckLimiters)
		}
	}()
}

func getLimiter(m *sync.Map, mu *sync.Mutex, key string, newLimiter func() *rate.Limiter) *rate.Limiter {
	if v, ok := m.Load(key); ok {
		e := v.(*limiterEntry)
		e.lastSeen.Store(time.Now().Unix())
		return e.l
	}
	mu.Lock()
	defer mu.Unlock()
	if v, ok := m.Load(key); ok {
		e := v.(*limiterEntry)
		e.lastSeen.Store(time.Now().Unix())
		return e.l
	}
	e := &limiterEntry{l: newLimiter()}
	e.lastSeen.Store(time.Now().Unix())
	m.Store(key, e)
	return e.l
}

// uploadLimiters stores per-user-per-channel rate limiters for file uploads.
// Key: "userEmail:channelSlug" — 30 uploads/min (one every 2s), burst of 10.
var (
	uploadLimiters  sync.Map
	uploadLimiterMu sync.Mutex
)

func getUploadLimiter(key string) *rate.Limiter {
	return getLimiter(&uploadLimiters, &uploadLimiterMu, key, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(2*time.Second), 10)
	})
}

// channelRequestLimiters throttles the public channel-request endpoint per
// client IP — 3 submissions/min, burst of 3.
var (
	channelRequestLimiters sync.Map
	channelRequestMu       sync.Mutex
)

func channelRequestLimiter(r *http.Request) *rate.Limiter {
	key, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		key = r.RemoteAddr
	}
	// An IPv6 client can trivially hop across its routed /64, minting unlimited
	// distinct addresses; key the whole prefix as one client.
	if addr, perr := netip.ParseAddr(key); perr == nil && addr.Is6() && !addr.Is4In6() {
		if p, perr := addr.Prefix(64); perr == nil {
			key = p.Masked().String()
		}
	}

	return getLimiter(&channelRequestLimiters, &channelRequestMu, key, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(20*time.Second), 3)
	})
}

// channelCreateLimiters throttles self-service channel creation per session
// user — 3 creations/hour, burst of 3. Creation is cheap for the caller and
// permanent for us, so the window is much wider than the upload limiter's.
var (
	channelCreateLimiters sync.Map
	channelCreateMu       sync.Mutex
)

func channelCreateLimiter(email string) *rate.Limiter {
	return getLimiter(&channelCreateLimiters, &channelCreateMu, email, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(20*time.Minute), 3)
	})
}

// slugCheckLimiters throttles the slug-availability probe per session user.
// It is a single EXISTS, but it is also a slug enumeration oracle, so it is
// generous rather than free — 60 checks/min (one per second), burst of 10.
var (
	slugCheckLimiters sync.Map
	slugCheckMu       sync.Mutex
)

func slugCheckLimiter(email string) *rate.Limiter {
	return getLimiter(&slugCheckLimiters, &slugCheckMu, email, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(time.Second), 10)
	})
}

func uploadRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := channelSlugFromCtx(r)

		// Key by user identity so limits are per-user, not per-channel
		session, _ := store.Get(r, cookieName)
		user, _ := session.Values["user"].(Session)
		key := user.Email + ":" + slug
		if key == ":" {
			key = slug // unauthenticated fallback (shouldn't happen, upload requires login)
		}

		if !getUploadLimiter(key).Allow() {
			http.Error(w, "too many uploads — please slow down", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
