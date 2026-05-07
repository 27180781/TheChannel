package main

import (
	"encoding/json"
	"net/http"
)

type AdsSettings struct {
	Src   string `json:"src"`
	Width int64  `json:"width"`
}

func getAdsSettings(w http.ResponseWriter, r *http.Request) {
	settings := AdsSettings{
		Src:   settingConfig.AdSrc,
		Width: settingConfig.AdWidth,
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
	settings := MagnetAdsSettings{
		Enabled:              settingConfig.MagnetEnabled,
		Mode:                 settingConfig.MagnetMode,
		PerMessages:          settingConfig.MagnetPerMessages,
		MinTimeSeconds:       settingConfig.MagnetMinTimeSeconds,
		PerSeconds:           settingConfig.MagnetPerSeconds,
		MinMessagesSinceLast: settingConfig.MagnetMinMessagesSince,
	}

	if settings.Enabled {
		settings.Snippet = settingConfig.MagnetSnippet
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}
