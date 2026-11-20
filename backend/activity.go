package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Channel momentum: what keeps a new channel from going quiet.
//
// A channel is only worth something once it has posts, readers and, ideally,
// more than one person writing. Most channels that stop do so in the first
// days: opened, nothing posted, nobody invited. So the platform now
//
//   - tells the channel's staff where the channel stands (GET .../activity):
//     the feed and the manage page show the next steps and cheer each
//     milestone (frontend: channel-momentum, celebration);
//   - mails the owner, through Magnet like every other channel mail, when the
//     channel is stuck: nothing posted two days after opening, no readers yet
//     four days after the first post, or no post for two weeks.
//
// The mails are rare on purpose. Each reason goes out at most once per
// channel (the dormant one once per quiet spell), never two within
// nudgeMinGap of each other, only to the owner, and every mail carries a
// one-click link that turns them off for that channel. That link is signed
// with the mail secret, so it cannot be forged for someone else's channel,
// and owners can also switch the mails back on from the manage page.
//
// Keys:
//
//	channel:<slug>:nudges  HASH  kind -> unix time it was sent, "last" -> last send,
//	                             "off" -> "1" when the owner turned the mails off

const (
	nudgeNoMessages    = "no_messages"
	nudgeNoSubscribers = "no_subscribers"
	nudgeInactive      = "inactive"

	magnetMailEventChannelNudge = "channel_nudge"

	nudgeNoMessagesAfter    = 2 * 24 * time.Hour
	nudgeNoSubscribersAfter = 4 * 24 * time.Hour
	nudgeInactiveAfter      = 14 * 24 * time.Hour
	// nudgeInactiveRepeat: a channel that stays quiet hears from us again
	// only after this long, whatever happens in between.
	nudgeInactiveRepeat = 30 * 24 * time.Hour
	nudgeMinGap         = 3 * 24 * time.Hour
	// nudgeGiveUpAfter: a channel abandoned for longer than this is left
	// alone. A first mail about a channel the owner forgot months ago reads
	// as spam, not as help.
	nudgeGiveUpAfter = 120 * 24 * time.Hour

	// nudgeMaxPerRun spreads a first run over a large backlog of quiet
	// channels across hours instead of one burst from the mail domain.
	nudgeMaxPerRun = 40
	nudgeInterval  = time.Hour
	nudgeLockKey   = "nudges:lock"
)

var (
	nudgesDisabled = strings.TrimSpace(os.Getenv("CHANNEL_NUDGES_DISABLED")) != ""
	// nudgeNow is the clock the rules read; a var so tests can move it.
	nudgeNow = time.Now
)

func nudgesKey(slug string) string { return fmt.Sprintf("channel:%s:nudges", slug) }

// channelActivity is what the momentum panel and the nudge rules read.
type channelActivity struct {
	CreatedAt     time.Time  `json:"createdAt"`
	Messages      int64      `json:"messages"`
	LastMessageAt *time.Time `json:"lastMessageAt"`
	// Subscribers: signed-in readers who ever opened the channel — the same
	// number the statistics screen and "my channels" call participants.
	Subscribers int64 `json:"subscribers"`
	// Push: devices subscribed to the channel's notifications.
	Push int64 `json:"push"`
	// Team: writers and managers besides the owners.
	Team   int64 `json:"team"`
	Owners int64 `json:"owners"`
	// MailsOff: the owner turned the reminder mails off for this channel.
	MailsOff bool   `json:"mailsOff"`
	ShareURL string `json:"shareUrl"`
}

// channelTeam counts the channel's staff from the in-memory privileges map,
// which is what every role check reads, and returns the owners' emails.
func channelTeam(slug string) (team int64, owners []string) {
	privilegesUsers.Range(func(_, v any) bool {
		u, ok := v.(User)
		if !ok {
			return true
		}
		switch u.ChannelRoles[slug] {
		case RoleOwner:
			owners = append(owners, normEmail(u.Email))
		case RoleModerator, RoleWriter:
			team++
		}
		return true
	})
	sort.Strings(owners)
	return team, owners
}

func loadChannelActivity(ctx context.Context, ch *ChannelData) (*channelActivity, error) {
	slug := ch.Slug
	pipe := rdb.Pipeline()
	count := pipe.ZCard(ctx, fmt.Sprintf("channel:%s:m_times", slug))
	last := pipe.ZRevRangeWithScores(ctx, fmt.Sprintf("channel:%s:m_times", slug), 0, 0)
	subs := pipe.SCard(ctx, fmt.Sprintf("channel:%s:registered_emails", slug))
	push := pipe.SCard(ctx, fmt.Sprintf("channel:%s:subscriptions", slug))
	off := pipe.HGet(ctx, nudgesKey(slug), "off")
	// HGet of an absent field is redis.Nil, which Exec reports; every other
	// command's own error is checked below.
	pipe.Exec(ctx)
	for _, c := range []interface{ Err() error }{count, last, subs} {
		if err := c.Err(); err != nil {
			return nil, err
		}
	}
	a := &channelActivity{
		CreatedAt:   ch.CreatedAt,
		Messages:    count.Val(),
		Subscribers: subs.Val(),
		Push:        push.Val(),
		MailsOff:    off.Val() == "1",
		ShareURL:    publicBaseURL() + "/channel/" + url.PathEscape(slug),
	}
	if zs := last.Val(); len(zs) > 0 {
		t := time.Unix(int64(zs[0].Score), 0).UTC()
		// A post scheduled into the future must not make the channel look
		// active today.
		if now := nudgeNow(); t.After(now) {
			t = now.UTC()
		}
		a.LastMessageAt = &t
	}
	team, owners := channelTeam(slug)
	a.Team = team
	a.Owners = int64(len(owners))
	return a, nil
}

// getChannelActivity answers GET /api/channel/{slug}/admin/activity.
func getChannelActivity(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	ch := channelFromCtx(r)
	if ch == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	a, err := loadChannelActivity(ctx, ch)
	if err != nil {
		log.Printf("activity %s: %v", ch.Slug, err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(a)
}

// setChannelNudgeMails answers POST /api/channel/{slug}/admin/activity/mails
// {"off": bool} — the owner's switch for the reminder mails.
func setChannelNudgeMails(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	var body struct {
		Off bool `json:"off"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	slug := channelSlugFromCtx(r)
	if err := setNudgeMailsOff(ctx, slug, body.Off); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true, "mailsOff": body.Off})
}

func setNudgeMailsOff(ctx context.Context, slug string, off bool) error {
	if off {
		return rdb.HSet(ctx, nudgesKey(slug), "off", "1").Err()
	}
	return rdb.HDel(ctx, nudgesKey(slug), "off").Err()
}

// ── one-click opt-out ───────────────────────────────────────────────────────

// nudgeOptOutToken signs the channel slug with the mail secret: the link in
// the mail turns the reminders off without signing in, and nobody can build
// one for a channel they did not get a mail about.
func nudgeOptOutToken(slug string) string {
	m := hmac.New(sha256.New, []byte("channel-nudges-off:"+magnetMailSecret))
	m.Write([]byte(slug))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

func nudgeOptOutURL(base, slug string) string {
	q := url.Values{"c": {slug}, "t": {nudgeOptOutToken(slug)}}
	return base + "/api/channel-reminders/off?" + q.Encode()
}

var nudgeOptOutPage = template.Must(template.New("off").Parse(`<!doctype html>
<html lang="he" dir="rtl"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{.Title}}</title>
<style>body{margin:0;background:#eef1f6;font-family:Arial,Helvetica,sans-serif;color:#1f2937}
main{max-width:480px;margin:48px auto;padding:32px 24px;background:#fff;border-radius:14px;text-align:center;box-shadow:0 1px 3px rgba(0,0,0,.08)}
h1{font-size:22px;margin:0 0 12px}p{font-size:16px;line-height:1.6;margin:0 0 20px;color:#4b5563}
a{display:inline-block;background:#1d4fd7;color:#fff;text-decoration:none;padding:12px 24px;border-radius:8px;font-weight:bold}</style>
</head><body><main><h1>{{.Title}}</h1><p>{{.Text}}</p>{{if .Link}}<a href="{{.Link}}">לניהול הערוץ</a>{{end}}</main></body></html>`))

// channelRemindersOff answers GET /api/channel-reminders/off?c=<slug>&t=<token>.
func channelRemindersOff(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	slug := r.URL.Query().Get("c")
	token := r.URL.Query().Get("t")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	page := struct{ Title, Text, Link string }{}
	if magnetMailSecret == "" || !isPlausibleSlug(slug) ||
		!hmac.Equal([]byte(token), []byte(nudgeOptOutToken(slug))) {
		w.WriteHeader(http.StatusBadRequest)
		page.Title = "הקישור אינו תקין"
		page.Text = "אפשר לכבות את התזכורות גם ממסך הניהול של הערוץ."
		nudgeOptOutPage.Execute(w, page)
		return
	}
	if exists, err := dbChannelExists(ctx, slug); err != nil || !exists {
		w.WriteHeader(http.StatusNotFound)
		page.Title = "הערוץ לא נמצא"
		page.Text = "ייתכן שהערוץ נמחק. לא יישלחו עליו תזכורות."
		nudgeOptOutPage.Execute(w, page)
		return
	}
	if err := setNudgeMailsOff(ctx, slug, true); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		page.Title = "משהו השתבש"
		page.Text = "לא הצלחנו לשמור את הבקשה. נסו שוב בעוד רגע."
		nudgeOptOutPage.Execute(w, page)
		return
	}
	page.Title = "התזכורות כובו"
	page.Text = "לא נשלח לך יותר מייל תזכורת על הערוץ הזה. אפשר להפעיל אותן מחדש ממסך הניהול, בלשונית פרטי הערוץ."
	page.Link = publicBaseURL() + "/channel/" + url.PathEscape(slug) + "/manage/info"
	nudgeOptOutPage.Execute(w, page)
}

// ── the reminder mails ──────────────────────────────────────────────────────

type magnetMailNudge struct {
	Kind        string `json:"kind"`
	Days        int    `json:"days"`
	Messages    int64  `json:"messages"`
	Subscribers int64  `json:"subscribers"`
	ShareURL    string `json:"share_url"`
	OptOutURL   string `json:"opt_out_url"`
}

// nudgeState is the channel's nudges hash, parsed.
type nudgeState struct {
	sent map[string]time.Time
	last time.Time
	off  bool
}

func parseNudgeState(h map[string]string) nudgeState {
	s := nudgeState{sent: map[string]time.Time{}, off: h["off"] == "1"}
	for k, v := range h {
		var unix int64
		if _, err := fmt.Sscan(v, &unix); err != nil || unix <= 0 {
			continue
		}
		t := time.Unix(unix, 0)
		if k == "last" {
			s.last = t
		} else {
			s.sent[k] = t
		}
	}
	return s
}

// pickNudge decides which reminder, if any, a channel is due. It is pure, so
// the rules are pinned by tests. The second value is the idempotency key's
// suffix: the dormant reminder is keyed by the post it follows, so each quiet
// spell is one mail, and Magnet refuses a repeat of the same key.
func pickNudge(now time.Time, a *channelActivity, st nudgeState) (kind string, key string, days int) {
	if st.off || a.CreatedAt.IsZero() {
		return "", "", 0
	}
	if !st.last.IsZero() && now.Sub(st.last) < nudgeMinGap {
		return "", "", 0
	}
	age := now.Sub(a.CreatedAt)
	switch {
	case a.Messages == 0:
		if age >= nudgeNoMessagesAfter && age < nudgeGiveUpAfter && st.sent[nudgeNoMessages].IsZero() {
			return nudgeNoMessages, nudgeNoMessages, int(age.Hours() / 24)
		}
	case a.LastMessageAt != nil && now.Sub(*a.LastMessageAt) >= nudgeInactiveAfter:
		if now.Sub(*a.LastMessageAt) >= nudgeGiveUpAfter {
			break
		}
		prev := st.sent[nudgeInactive]
		if prev.IsZero() || now.Sub(prev) >= nudgeInactiveRepeat {
			return nudgeInactive, fmt.Sprintf("%s:%d", nudgeInactive, a.LastMessageAt.Unix()),
				int(now.Sub(*a.LastMessageAt).Hours() / 24)
		}
	case a.Subscribers <= a.Owners && age >= nudgeNoSubscribersAfter && age < nudgeGiveUpAfter:
		// Nobody but the owners themselves has opened the channel yet.
		if st.sent[nudgeNoSubscribers].IsZero() {
			return nudgeNoSubscribers, nudgeNoSubscribers, int(age.Hours() / 24)
		}
	}
	return "", "", 0
}

func channelNudgeMail(ch *ChannelData, a *channelActivity, kind, key string, days int, to, toName string) *magnetMailEvent {
	base := publicBaseURL()
	return &magnetMailEvent{
		Event:          magnetMailEventChannelNudge,
		IdempotencyKey: capKey("channel_nudge:" + ch.Slug + ":" + key),
		To:             to,
		ToName:         mailToName(toName, to),
		Channel:        mailChannel(base, ch),
		ContactURL:     supportContactURL(base, ch.Slug),
		Nudge: &magnetMailNudge{
			Kind:        kind,
			Days:        days,
			Messages:    a.Messages,
			Subscribers: a.Subscribers,
			ShareURL:    a.ShareURL,
			OptOutURL:   nudgeOptOutURL(base, ch.Slug),
		},
	}
}

// nudgeRecipient is the channel's owner: the address it was opened with, or
// else the first owner on the staff list (a channel the super admin opened
// for someone has no ownerEmail).
func nudgeRecipient(ch *ChannelData) string {
	if e := normEmail(ch.OwnerEmail); looksLikeEmail(e) && emailHasChannelRole(e, ch.Slug, RoleOwner) {
		return e
	}
	_, owners := channelTeam(ch.Slug)
	for _, e := range owners {
		if looksLikeEmail(e) {
			return e
		}
	}
	return ""
}

// runNudges is one pass over every channel. It returns how many mails went out.
func runNudges(ctx context.Context) int {
	channels, err := dbListChannels(ctx)
	if err != nil {
		log.Printf("nudges: list channels: %v", err)
		return 0
	}
	now := nudgeNow()
	sent := 0
	for _, ch := range channels {
		if sent >= nudgeMaxPerRun || ctx.Err() != nil {
			break
		}
		if ch == nil || ch.Features.Disabled {
			continue
		}
		h, err := rdb.HGetAll(ctx, nudgesKey(ch.Slug)).Result()
		if err != nil {
			continue
		}
		st := parseNudgeState(h)
		if st.off {
			continue
		}
		a, err := loadChannelActivity(ctx, ch)
		if err != nil {
			continue
		}
		kind, key, days := pickNudge(now, a, st)
		if kind == "" {
			continue
		}
		to := nudgeRecipient(ch)
		if to == "" {
			continue
		}
		name := ""
		if c, err := dbGetOwnerConsent(ctx, to); err == nil && c != nil {
			name = c.Name
		}
		ev := channelNudgeMail(ch, a, kind, key, days, to, name)
		if err := deliverMagnetMail(ctx, ev); err != nil {
			log.Printf("nudges: %s %s not delivered: %v", ch.Slug, kind, err)
			continue
		}
		ts := now.Unix()
		rdb.HSet(ctx, nudgesKey(ch.Slug), kind, ts, "last", ts)
		sent++
	}
	return sent
}

var nudgesStart sync.Once

// startNudges runs the reminder pass every hour on one instance at a time.
// Called from main once Redis is up; does nothing when mail is off.
func startNudges() {
	nudgesStart.Do(func() {
		if nudgesDisabled {
			log.Println("nudges: CHANNEL_NUDGES_DISABLED is set — reminder mails are off")
			return
		}
		go func() {
			// Let the process settle (privileges loaded, config read) first.
			time.Sleep(2 * time.Minute)
			for {
				if magnetMailEnabled() {
					nudgeTick()
				}
				time.Sleep(nudgeInterval)
			}
		}()
	})
}

func nudgeTick() {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("nudges: panic: %v", p)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	// One instance per hour: the lock outlives the pass, so a second
	// instance waking a minute later finds it taken and skips.
	ok, err := rdb.SetNX(ctx, nudgeLockKey, sseInstanceID, nudgeInterval-5*time.Minute).Result()
	if err != nil || !ok {
		return
	}
	if n := runNudges(ctx); n > 0 {
		log.Printf("nudges: %d reminder mails sent", n)
	}
}
