package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi"
)

func addonsChannel(t *testing.T, slug string) {
	t.Helper()
	ctx := context.Background()
	rdb.HSet(ctx, "channel:"+slug, "slug", slug, "name", "ערוץ בדיקה")
	t.Cleanup(func() { rdb.Del(context.Background(), "channel:"+slug) })
}

func putAddons(t *testing.T, secret, slug, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/integrations/magnet/admin/channels/"+slug+"/addons", strings.NewReader(body))
	req.Header.Set(magnetAdminSecretHeader, secret)
	req.Header.Set(magnetAdminOperatorHeader, "channels-billing")
	rec := httptest.NewRecorder()
	addonsRouter().ServeHTTP(rec, req)
	return rec.Code
}

func addonsRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/api/integrations/magnet/admin", func(r chi.Router) {
		r.Use(requireMagnetAdmin)
		r.Put("/channels/{slug}/addons", magnetSetChannelAddons)
	})
	return r
}

// Magnet's push stores the dates; null clears one; the door's secret guards it;
// a channel that does not exist is not recreated.
func TestMagnetPushesAddonDates(t *testing.T) {
	withMagnetAdminSecret(t, "addon-secret")
	const slug = "addons-push"
	addonsChannel(t, slug)
	future := time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)

	if code := putAddons(t, "wrong", slug, `{}`); code != http.StatusNotFound {
		t.Fatalf("wrong secret: %d, want 404", code)
	}
	if code := putAddons(t, "addon-secret", slug, `{"noCreditUntil":"`+future+`","noAdsUntil":null}`); code != http.StatusOK {
		t.Fatalf("push: %d", code)
	}
	ch, err := dbGetChannel(context.Background(), slug)
	if err != nil {
		t.Fatal(err)
	}
	if !channelNoCredit(ch) || channelNoAds(ch) {
		t.Fatalf("after push: noCredit %v noAds %v", channelNoCredit(ch), channelNoAds(ch))
	}

	// Magnet's own timestamptz form, with an offset and fractions.
	if code := putAddons(t, "addon-secret", slug, `{"noCreditUntil":null,"noAdsUntil":"2020-01-01T10:00:00.123456+02:00"}`); code != http.StatusOK {
		t.Fatalf("second push: %d", code)
	}
	ch, _ = dbGetChannel(context.Background(), slug)
	if channelNoCredit(ch) || channelNoAds(ch) || ch.NoAdsUntil.IsZero() {
		t.Fatalf("a cleared and a past date must both be off: %+v", ch)
	}

	if code := putAddons(t, "addon-secret", slug, `{"noAdsUntil":"next week"}`); code != http.StatusBadRequest {
		t.Fatalf("bad date: %d, want 400", code)
	}
	if code := putAddons(t, "addon-secret", "addons-nobody", `{"noAdsUntil":"`+future+`"}`); code != http.StatusNotFound {
		t.Fatalf("unknown channel: %d, want 404", code)
	}
	if n, _ := rdb.Exists(context.Background(), "channel:addons-nobody").Result(); n != 0 {
		t.Fatal("a push recreated a deleted channel's hash")
	}
}

// no_ads lifts both platform locks, and only while it is paid.
func TestNoAdsLiftsThePlatformLocks(t *testing.T) {
	ads := &GlobalAdsConfig{LockAll: true}
	magnet := &GlobalMagnetConfig{LockAll: true}
	ch := &ChannelData{Slug: "x", Features: ChannelFeatures{AdsLockedByAdmin: true, MagnetLockedByAdmin: true}}
	if !isChannelAdsLocked(ads, ch) || !isChannelMagnetLocked(magnet, ch) {
		t.Fatal("unpaid channel must stay locked")
	}
	ch.NoAdsUntil = time.Now().Add(time.Hour)
	if isChannelAdsLocked(ads, ch) || isChannelMagnetLocked(magnet, ch) {
		t.Fatal("no_ads must lift both locks")
	}
	ch.NoAdsUntil = time.Now().Add(-time.Minute)
	if !isChannelAdsLocked(ads, ch) || !isChannelMagnetLocked(magnet, ch) {
		t.Fatal("an expired no_ads must lock again")
	}
	if isChannelAdsLocked(&GlobalAdsConfig{}, nil) {
		t.Fatal("nil channel")
	}
}

// fakeBilling stands in for channels-billing and records what it was sent.
func fakeBilling(t *testing.T, answer func(op string) (int, string)) *[]map[string]any {
	t.Helper()
	var mu sync.Mutex
	got := []map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(magnetAdminSecretHeader) != magnetAdminSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		json.Unmarshal(raw, &b)
		mu.Lock()
		got = append(got, b)
		mu.Unlock()
		code, body := answer(b["op"].(string))
		w.WriteHeader(code)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	old := magnetBillingURL
	magnetBillingURL = srv.URL
	t.Cleanup(func() { magnetBillingURL = old })
	return &got
}

// The owner's start names the channel and the payer from the session, not
// from the browser, and refuses without the current terms.
func TestOwnerStartUsesSessionAndTerms(t *testing.T) {
	useTestSessionStore(t)
	withMagnetAdminSecret(t, "addon-secret")
	const slug = "addons-start"
	addonsChannel(t, slug)
	sent := fakeBilling(t, func(op string) (int, string) {
		return 200, `{"subscriptionId":"s1","transactionId":"9001","key":"abcdefgh","amountGross":30,"description":"מנוי"}`
	})
	sess := Session{ID: "ao1", Email: "Owner@Example.com", PublicName: "משה", ChannelRoles: map[string]ChannelRole{slug: RoleOwner}}

	call := func(body string) *httptest.ResponseRecorder {
		r := requestAs(t, sess, slug, http.MethodPost, "/api/channel/"+slug+"/admin/addons/start", strings.NewReader(body), nil)
		w := httptest.NewRecorder()
		startChannelAddons(w, r)
		return w
	}
	if w := call(`{"addons":["no_ads"],"acceptedTerms":false,"termsVersion":"` + addonTermsVersion + `"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("without terms: %d", w.Code)
	}
	if w := call(`{"addons":["no_ads"],"acceptedTerms":true,"termsVersion":"old"}`); w.Code != http.StatusConflict {
		t.Fatalf("old terms: %d", w.Code)
	}
	if len(*sent) != 0 {
		t.Fatal("a refused start reached Magnet")
	}
	w := call(`{"addons":["no_ads","no_credit"],"zeout":"890108558","phone":"0501234567","acceptedTerms":true,"termsVersion":"` + addonTermsVersion + `","payerEmail":"evil@example.com","slug":"other"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	b := (*sent)[0]
	if b["op"] != "start" || b["slug"] != slug || b["payerEmail"] != "owner@example.com" || b["payerName"] != "משה" ||
		b["termsVersion"] != addonTermsVersion || b["acceptedTerms"] != true {
		t.Fatalf("sent to Magnet: %v", b)
	}
}

// Magnet's Hebrew refusal reaches the owner; its failure does not leak.
func TestOwnerSeesMagnetRefusal(t *testing.T) {
	useTestSessionStore(t)
	withMagnetAdminSecret(t, "addon-secret")
	const slug = "addons-refuse"
	addonsChannel(t, slug)
	fakeBilling(t, func(op string) (int, string) {
		if op == "cancel" {
			return 404, `{"error":"not_found","message":"המנוי לא נמצא"}`
		}
		return 500, `{"error":"server_error","detail":"secret stuff"}`
	})
	sess := Session{ID: "ao2", Email: "o@example.com", ChannelRoles: map[string]ChannelRole{slug: RoleOwner}}

	r := requestAs(t, sess, slug, http.MethodPost, "/x", strings.NewReader(`{"subscriptionId":"5ab0c0de-4444-4444-4444-444444444444"}`), nil)
	w := httptest.NewRecorder()
	cancelChannelAddons(w, r)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "המנוי לא נמצא") {
		t.Fatalf("cancel refusal: %d %s", w.Code, w.Body.String())
	}

	r = requestAs(t, sess, slug, http.MethodGet, "/x", nil, nil)
	w = httptest.NewRecorder()
	getChannelAddons(w, r)
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("status failure: %d %s", w.Code, w.Body.String())
	}
}

// The status refreshes the stored dates and carries the terms version.
func TestOwnerStatusRefreshesDates(t *testing.T) {
	useTestSessionStore(t)
	withMagnetAdminSecret(t, "addon-secret")
	const slug = "addons-status"
	addonsChannel(t, slug)
	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	fakeBilling(t, func(op string) (int, string) {
		return 200, `{"price":15,"entitlement":{"no_credit":null,"no_ads":"` + future + `"},"subscriptions":[]}`
	})
	sess := Session{ID: "ao3", Email: "o@example.com", ChannelRoles: map[string]ChannelRole{slug: RoleOwner}}
	r := requestAs(t, sess, slug, http.MethodGet, "/x", nil, nil)
	w := httptest.NewRecorder()
	getChannelAddons(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"termsVersion":"`+addonTermsVersion+`"`) {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	ch, _ := dbGetChannel(context.Background(), slug)
	if !channelNoAds(ch) || channelNoCredit(ch) {
		t.Fatalf("dates not refreshed: %+v", ch)
	}
}
