package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

// Paid add-ons for channel owners (Magnet's SPEC §66). Two of them, bought
// monthly by the owner in the channel's management screen:
//
//	no_credit — the platform credit ("הוקם ע״י…") is not shown in the header;
//	no_ads    — the platform's ads are not served: neither the super admin's
//	            frame ad nor the Magnet embed the super admin locked in. The
//	            channel's own settings apply as if it were not locked.
//
// The money lives in Magnet. Its channels-billing function opens the Nedarim
// standing order, records every charge, issues the receipt and decides until
// when each add-on is paid. This side only keeps that date per add-on, in the
// channel hash, and checks it against the clock:
//
//	addonNoCreditUntil, addonNoAdsUntil   RFC 3339, absent when never paid
//
// so an add-on whose renewal never came ends by itself, even if Magnet's next
// push is lost. Magnet pushes the dates through the admin door
// (PUT /api/integrations/magnet/admin/channels/{slug}/addons) after every
// change, and the owner's screen refreshes them whenever it loads.
//
// The owner's endpoints below relay to channels-billing with
// MAGNET_ADMIN_SECRET, the secret Magnet already uses for the door in the
// other direction, and name the channel and the signed-in owner themselves:
// the browser never chooses whose channel or which payer.

const (
	addonHashNoCredit = "addonNoCreditUntil"
	addonHashNoAds    = "addonNoAdsUntil"

	maxAddonBody = 8 << 10
)

// addonTermsVersion is the Revised date of /upgrade-terms (legal.go), the
// purchase terms the checkout links to. The status hands it to the page and
// the start insists on it, so a checkout opened before the terms changed is
// refused and nobody pays under wording they were not shown.
var addonTermsVersion = legalRevised("/upgrade-terms")

var (
	magnetBillingURL    = magnetBillingURLFromEnv()
	magnetBillingClient = &http.Client{Timeout: 25 * time.Second}
)

// magnetBillingURLFromEnv is MAGNET_BILLING_URL, or else the mail endpoint's
// sibling function: both are Edge Functions of the same Magnet project.
func magnetBillingURLFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("MAGNET_BILLING_URL")); v != "" {
		return v
	}
	mail := strings.TrimRight(strings.TrimSpace(os.Getenv("MAGNET_MAIL_URL")), "/")
	if strings.HasSuffix(mail, "/channels-mail") {
		return strings.TrimSuffix(mail, "channels-mail") + "channels-billing"
	}
	return ""
}

func magnetBillingEnabled() bool {
	return magnetBillingURL != "" && magnetAdminSecret != ""
}

// addonActive: paid until a moment still ahead.
func addonActive(until time.Time) bool {
	return !until.IsZero() && time.Now().Before(until)
}

// channelNoAds is the no_ads add-on in force on ch.
func channelNoAds(ch *ChannelData) bool {
	return ch != nil && addonActive(ch.NoAdsUntil)
}

// channelNoCredit is the no_credit add-on in force on ch.
func channelNoCredit(ch *ChannelData) bool {
	return ch != nil && addonActive(ch.NoCreditUntil)
}

// parseAddonTime reads one hash field; anything unreadable counts as unpaid.
func parseAddonTime(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

// addonDates is the wire form both directions use: an RFC 3339 moment, or
// null for "not paid".
type addonDates struct {
	NoCreditUntil *string `json:"noCreditUntil"`
	NoAdsUntil    *string `json:"noAdsUntil"`
}

// storeAddonDates writes Magnet's dates into the channel hash. A null removes
// the field. The channel must exist: a push for a deleted channel must not
// recreate its hash with nothing but add-on fields in it.
func storeAddonDates(ctx context.Context, slug string, d addonDates) error {
	key := "channel:" + slug
	n, err := rdb.Exists(ctx, key).Result()
	if err != nil {
		return err
	}
	if n == 0 {
		return redis.Nil
	}
	set := []any{}
	del := []string{}
	for _, f := range []struct {
		field string
		v     *string
	}{{addonHashNoCredit, d.NoCreditUntil}, {addonHashNoAds, d.NoAdsUntil}} {
		if f.v == nil || strings.TrimSpace(*f.v) == "" {
			del = append(del, f.field)
			continue
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*f.v))
		if err != nil {
			return errBadAddonDate
		}
		set = append(set, f.field, t.UTC().Format(time.RFC3339))
	}
	pipe := rdb.TxPipeline()
	if len(set) > 0 {
		pipe.HSet(ctx, key, set...)
	}
	if len(del) > 0 {
		pipe.HDel(ctx, key, del...)
	}
	_, err = pipe.Exec(ctx)
	return err
}

var errBadAddonDate = errors.New("bad add-on date")

// magnetSetChannelAddons – Magnet admin door: the paid-until dates of one
// channel, pushed by channels-billing after every change.
func magnetSetChannelAddons(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	slug := chi.URLParam(r, "slug")
	if !isPlausibleSlug(slug) {
		http.NotFound(w, r)
		return
	}
	var d addonDates
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAddonBody)).Decode(&d); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_body", "body must be JSON")
		return
	}
	switch err := storeAddonDates(ctx, slug, d); {
	case errors.Is(err, redis.Nil):
		writeJSONError(w, http.StatusNotFound, "not_found", "channel not found")
		return
	case errors.Is(err, errBadAddonDate):
		writeJSONError(w, http.StatusBadRequest, "bad_date", "dates must be RFC 3339")
		return
	case err != nil:
		log.Printf("channel addons: %s: storing the dates failed: %v", slug, err)
		writeJSONError(w, http.StatusInternalServerError, "store_failed", "error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}

// callMagnetBilling posts one owner operation to channels-billing and returns
// its status and body as they came.
func callMagnetBilling(ctx context.Context, body map[string]any) (int, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, magnetBillingURL, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(magnetAdminSecretHeader, magnetAdminSecret)
	res, err := magnetBillingClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	out, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, out, err
}

const addonsUnavailable = "מערכת התשלומים לא זמינה כרגע. נסו שוב בעוד כמה דקות."

// relayMagnetBilling answers the browser with channels-billing's answer: its
// Hebrew refusal on a 4xx, a generic message on anything worse.
func relayMagnetBilling(w http.ResponseWriter, ctx context.Context, op string, body map[string]any) ([]byte, bool) {
	if !magnetBillingEnabled() {
		writeJSONError(w, http.StatusServiceUnavailable, "billing_off", addonsUnavailable)
		return nil, false
	}
	body["op"] = op
	code, out, err := callMagnetBilling(ctx, body)
	if err != nil || code >= 500 || code == http.StatusUnauthorized {
		log.Printf("channel addons: %s for %v: status %d, err %v", op, body["slug"], code, err)
		writeJSONError(w, http.StatusBadGateway, "billing_failed", addonsUnavailable)
		return nil, false
	}
	if code >= 400 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		json.Unmarshal(out, &e)
		if e.Message == "" {
			e.Message = "הבקשה לא התקבלה"
		}
		writeJSONError(w, code, e.Error, e.Message)
		return nil, false
	}
	return out, true
}

// getChannelAddons – owner: the price, what is in force, and the channel's
// subscriptions with their charges and receipts. Also refreshes the stored
// dates, so a lost push heals the moment the owner looks.
func getChannelAddons(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	slug := channelSlugFromCtx(r)

	out, ok := relayMagnetBilling(w, ctx, "status", map[string]any{"slug": slug})
	if !ok {
		return
	}
	var st struct {
		Entitlement struct {
			NoCredit *string `json:"no_credit"`
			NoAds    *string `json:"no_ads"`
		} `json:"entitlement"`
	}
	if err := json.Unmarshal(out, &st); err == nil {
		if err := storeAddonDates(ctx, slug, addonDates{NoCreditUntil: st.Entitlement.NoCredit, NoAdsUntil: st.Entitlement.NoAds}); err != nil {
			log.Printf("channel addons: %s: refreshing the dates failed: %v", slug, err)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(addTermsVersion(out))
}

// addTermsVersion puts the terms version the server accepts into the status,
// so a page loaded before the terms changed is told to reload.
func addTermsVersion(out []byte) []byte {
	var m map[string]any
	if json.Unmarshal(out, &m) != nil || m == nil {
		return out
	}
	m["termsVersion"] = addonTermsVersion
	b, err := json.Marshal(m)
	if err != nil {
		return out
	}
	return b
}

// getChannelAddonsBuyerForm – owner: which buyer details Nedarim requires.
func getChannelAddonsBuyerForm(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, ok := relayMagnetBilling(w, ctx, "buyer_form", map[string]any{"slug": channelSlugFromCtx(r)})
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// startChannelAddons – owner: open the monthly standing order. The payer is
// the signed-in owner and the channel is the one in the path; the browser
// sends only the add-ons, the card holder's details and the terms it showed.
func startChannelAddons(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		Addons        []string `json:"addons"`
		Zeout         string   `json:"zeout"`
		Phone         string   `json:"phone"`
		TaxID         string   `json:"taxId"`
		Street        string   `json:"street"`
		City          string   `json:"city"`
		PayerName     string   `json:"payerName"`
		AcceptedTerms bool     `json:"acceptedTerms"`
		TermsVersion  string   `json:"termsVersion"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAddonBody)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_body", "הבקשה לא תקינה")
		return
	}
	if !req.AcceptedTerms {
		writeJSONError(w, http.StatusBadRequest, "terms_not_accepted", "צריך לאשר את תקנון השדרוגים")
		return
	}
	if req.TermsVersion != addonTermsVersion {
		writeJSONError(w, http.StatusConflict, "terms_changed", "תקנון השדרוגים עודכן. רעננו את הדף וקראו אותו שוב.")
		return
	}
	slug := channelSlugFromCtx(r)
	name := ""
	if ch := channelFromCtx(r); ch != nil {
		name = ch.Name
	}
	payerName := strings.TrimSpace(req.PayerName)
	if payerName == "" {
		payerName = sess.PublicName
	}
	out, ok := relayMagnetBilling(w, ctx, "start", map[string]any{
		"slug":          slug,
		"channelName":   name,
		"addons":        req.Addons,
		"payerEmail":    normEmail(sess.Email),
		"payerName":     payerName,
		"zeout":         req.Zeout,
		"phone":         req.Phone,
		"taxId":         req.TaxID,
		"street":        req.Street,
		"city":          req.City,
		"acceptedTerms": true,
		"termsVersion":  addonTermsVersion,
	})
	if !ok {
		return
	}
	log.Printf("channel addons: %s: checkout opened by %s for %v", slug, normEmail(sess.Email), req.Addons)
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

// cancelChannelAddons – owner: stop a subscription. Magnet deletes the
// standing order at Nedarim; the add-on stays until the last charge's period
// ends.
func cancelChannelAddons(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		SubscriptionID string `json:"subscriptionId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAddonBody)).Decode(&req); err != nil || req.SubscriptionID == "" {
		writeJSONError(w, http.StatusBadRequest, "bad_body", "הבקשה לא תקינה")
		return
	}
	slug := channelSlugFromCtx(r)
	out, ok := relayMagnetBilling(w, ctx, "cancel", map[string]any{
		"slug":           slug,
		"subscriptionId": req.SubscriptionID,
		"by":             normEmail(sess.Email),
	})
	if !ok {
		return
	}
	log.Printf("channel addons: %s: subscription %s cancelled by %s", slug, req.SubscriptionID, normEmail(sess.Email))
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}
