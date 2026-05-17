package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

var openSSEConnections = atomic.Int64{}
var peakSSEConnections = &PeakSSEConnections{}
var peakMu = sync.Mutex{}

// Per-channel SSE counters
var channelSSEConnections sync.Map // slug -> *atomic.Int64
var channelPeakSSE sync.Map        // slug -> *PeakSSEConnections
var channelPeakMu sync.Map         // slug -> *sync.Mutex

type PeakSSEConnections struct {
	Value     int64     `json:"value" redis:"value"`
	Timestamp time.Time `json:"timestamp" redis:"timestamp"`
}
type Statistics struct {
	Data   []int64  `json:"date"`
	Labels []string `json:"labels"`
}

func getOrCreateChannelCounter(slug string) *atomic.Int64 {
	v, _ := channelSSEConnections.LoadOrStore(slug, &atomic.Int64{})
	return v.(*atomic.Int64)
}

func getOrCreateChannelPeak(slug string) (*PeakSSEConnections, *sync.Mutex) {
	mu, _ := channelPeakMu.LoadOrStore(slug, &sync.Mutex{})
	m := mu.(*sync.Mutex)

	peak, loaded := channelPeakSSE.LoadOrStore(slug, &PeakSSEConnections{})
	if !loaded {
		// Try to load from DB
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if p, err := dbGetPeakSSEConnections(ctx, slug); err == nil && p != nil {
			peak = p
			channelPeakSSE.Store(slug, p)
		}
	}
	return peak.(*PeakSSEConnections), m
}

func init() {
	// Global peak initialization (legacy - for backward compat)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to load from a default channel or just use zero values
	_ = ctx
	peakMu.Lock()
	defer peakMu.Unlock()
	// peakSSEConnections remains zero-valued if no default slug
}

func statLogger() {
	var old int64
	for {
		new := openSSEConnections.Load()
		if old != new {
			old = new
		}
		time.Sleep(5 * time.Minute)
	}
}

func increaseCounterSSE(slug string) {
	// Global counter
	openSSEConnections.Add(1)

	// Per-channel counter
	counter := getOrCreateChannelCounter(slug)
	newVal := counter.Add(1)

	peak, mu := getOrCreateChannelPeak(slug)
	mu.Lock()
	defer mu.Unlock()
	if newVal > peak.Value {
		peak.Value = newVal
		peak.Timestamp = time.Now()
		p := *peak
		go dbSavePeakSSEConnections(slug, &p)
	}

	// Save stat
	go dbSaveSSEStatistics(slug, newVal)
}

func decreaseCounterSSE(slug string) {
	openSSEConnections.Add(-1)

	counter := getOrCreateChannelCounter(slug)
	newVal := counter.Add(-1)
	go dbSaveSSEStatistics(slug, newVal)
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

	counter := getOrCreateChannelCounter(slug)
	peak, mu := getOrCreateChannelPeak(slug)

	mu.Lock()
	defer mu.Unlock()

	response := struct {
		UsersAmount           int64               `json:"usersAmount"`
		ConnectedUsersAmount  int64               `json:"connectedUsersAmount"`
		PeakSSEConnections    *PeakSSEConnections `json:"peakSSEConnections"`
		ConnectionsStatistics Statistics          `json:"connectionsStatistics"`
	}{
		UsersAmount:           amount,
		ConnectedUsersAmount:  counter.Load(),
		PeakSSEConnections:    peak,
		ConnectionsStatistics: *s,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func resetStatistics(w http.ResponseWriter, r *http.Request) {
	peakMu.Lock()
	defer peakMu.Unlock()
	peakSSEConnections.Value = 0
	peakSSEConnections.Timestamp = time.Time{}

	var response Response
	response.Success = true
	json.NewEncoder(w).Encode(response)
}
