package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

type AdsSettings struct {
	Src   string `json:"src"`
	Width int64  `json:"width"`
}

func getAdsSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	cfg := getChannelConfig(ctx, slug)

	settings := AdsSettings{
		Src:   cfg.AdSrc,
		Width: cfg.AdWidth,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

type MagnetAdsSettings struct {
	Enabled              bool   `json:"enabled"`
	Snippet              string `json:"snippet"`
	Mode                 string `json:"mode"`
	PerMessages          int64  `json:"perMessages"`
	MinTimeSeconds       int64  `json:"minTimeSeconds"`
	PerSeconds           int64  `json:"perSeconds"`
	MinMessagesSinceLast int64  `json:"minMessagesSinceLast"`
}

func getMagnetAdsSettings(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	cfg := getChannelConfig(ctx, slug)

	settings := MagnetAdsSettings{
		Enabled:              cfg.MagnetEnabled,
		Mode:                 cfg.MagnetMode,
		PerMessages:          cfg.MagnetPerMessages,
		MinTimeSeconds:       cfg.MagnetMinTimeSeconds,
		PerSeconds:           cfg.MagnetPerSeconds,
		MinMessagesSinceLast: cfg.MagnetMinMessagesSince,
	}

	if settings.Enabled {
		settings.Snippet = cfg.MagnetSnippet
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

const magnetStatsURL = "https://rucltqmtefvlrjhbedqu.supabase.co/functions/v1/publisher-stats"

var magnetStatsClient = &http.Client{Timeout: 15 * time.Second}

func getMagnetStats(w http.ResponseWriter, r *http.Request) {
	// Magnet stats uses global settingConfig
	apiKey := settingConfig.MagnetApiKey
	if apiKey == "" {
		http.Error(w, `{"error":"missing_api_key","message":"Magnet API key is not configured"}`, http.StatusBadRequest)
		return
	}

	q := url.Values{}
	q.Set("k", apiKey)
	reqURL := magnetStatsURL + "?" + q.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, reqURL, nil)
	if err != nil {
		http.Error(w, `{"error":"request_build_failed"}`, http.StatusInternalServerError)
		return
	}

	resp, err := magnetStatsClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"upstream_unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"upstream_read_failed"}`, http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}
