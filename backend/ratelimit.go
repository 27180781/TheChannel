package main

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// uploadLimiters stores a per-channel rate limiter for file uploads.
// Each channel allows up to 30 uploads per minute with a burst of 10.
var (
	uploadLimiters   sync.Map
	uploadLimiterMu  sync.Mutex
)

func getUploadLimiter(slug string) *rate.Limiter {
	if v, ok := uploadLimiters.Load(slug); ok {
		return v.(*rate.Limiter)
	}
	uploadLimiterMu.Lock()
	defer uploadLimiterMu.Unlock()
	// Double-check after lock
	if v, ok := uploadLimiters.Load(slug); ok {
		return v.(*rate.Limiter)
	}
	l := rate.NewLimiter(rate.Every(2*time.Second), 10) // 30/min, burst 10
	uploadLimiters.Store(slug, l)
	return l
}

func uploadRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := channelSlugFromCtx(r)
		if !getUploadLimiter(slug).Allow() {
			http.Error(w, "too many uploads — slow down", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}
