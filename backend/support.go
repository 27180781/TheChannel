package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

const (
	// supportTicketTTL bounds how long a ticket and its thread survive. Any
	// reply refreshes it, so an active conversation is never cut off mid-way.
	supportTicketTTL = 180 * 24 * time.Hour
	// maxSupportTicketsListed caps the admin inbox read, which does one GET per
	// entry — the same bound the channel-request queue uses.
	maxSupportTicketsListed = 500
	// maxSupportMessages caps a single thread. Without it one participant can
	// grow a record without limit, and every read of that ticket pays for it.
	maxSupportMessages = 100

	maxSupportSubjectLen = 200
	maxSupportBodyLen    = 5000
	maxSupportNameLen    = 100
	maxSupportEmailLen   = 254
)

type SupportStatus string

const (
	// Open: waiting on the operator. Answered: the operator replied and it is
	// with the requester. Closed: done, and no further replies are accepted.
	SupportStatusOpen     SupportStatus = "open"
	SupportStatusAnswered SupportStatus = "answered"
	SupportStatusClosed   SupportStatus = "closed"
)

func validSupportStatus(s SupportStatus) bool {
	switch s {
	case SupportStatusOpen, SupportStatusAnswered, SupportStatusClosed:
		return true
	}
	return false
}

// SupportMessage is one entry in a ticket thread.
type SupportMessage struct {
	// "user" or "admin" — who wrote it, for rendering the two sides.
	Author     string    `json:"author"`
	AuthorName string    `json:"authorName"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
}

type SupportTicket struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	// Set when the ticket was opened from a channel's admin panel, so the
	// operator knows which tenant is asking.
	ChannelSlug string `json:"channelSlug,omitempty"`
	// True when the sender had a session. A ticket from a signed-in user has a
	// trustworthy email; an anonymous one carries whatever was typed.
	Authenticated bool `json:"authenticated"`

	Status   SupportStatus    `json:"status"`
	Messages []SupportMessage `json:"messages"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// AccessToken lets the anonymous sender come back and read the reply. Every
	// response goes through publicView, which clears it, so it leaves the server
	// exactly once: in the reply to the request that created the ticket.
	AccessToken string `json:"accessToken,omitempty"`
}

const (
	supportTicketKeyPrefix = "support_ticket:"
	supportTicketIndexKey  = "support_tickets:list"
	// One index per email so a signed-in user can list their own tickets
	// without scanning the global index.
	supportUserIndexPrefix = "support_tickets:user:"
)

func supportTicketKey(id string) string { return supportTicketKeyPrefix + id }

func supportUserIndexKey(email string) string {
	return supportUserIndexPrefix + strings.ToLower(email)
}

func dbSaveSupportTicket(ctx context.Context, t *SupportTicket) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := rdb.Set(ctx, supportTicketKey(t.ID), data, supportTicketTTL).Err(); err != nil {
		return err
	}
	score := redis.Z{Score: float64(t.UpdatedAt.Unix()), Member: t.ID}
	if err := rdb.ZAdd(ctx, supportTicketIndexKey, score).Err(); err != nil {
		return err
	}
	if t.Email != "" {
		key := supportUserIndexKey(t.Email)
		if err := rdb.ZAdd(ctx, key, score).Err(); err != nil {
			return err
		}
		// The per-user index must not outlive the tickets it points at.
		rdb.Expire(ctx, key, supportTicketTTL)
	}
	return nil
}

func dbGetSupportTicket(ctx context.Context, id string) (*SupportTicket, error) {
	data, err := rdb.Get(ctx, supportTicketKey(id)).Result()
	if err != nil {
		return nil, err
	}
	var t SupportTicket
	if err := json.Unmarshal([]byte(data), &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// dbListSupportTickets reads an index newest-first, dropping entries whose
// bodies have already expired.
func dbListSupportTickets(ctx context.Context, indexKey string) ([]*SupportTicket, error) {
	cutoff := strconv.FormatInt(time.Now().Add(-supportTicketTTL).Unix(), 10)
	rdb.ZRemRangeByScore(ctx, indexKey, "-inf", cutoff)

	ids, err := rdb.ZRevRange(ctx, indexKey, 0, maxSupportTicketsListed-1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]*SupportTicket, 0, len(ids))
	for _, id := range ids {
		t, err := dbGetSupportTicket(ctx, id)
		if err != nil {
			// Expired body with a surviving index entry: drop it rather than
			// letting it reappear on every listing.
			rdb.ZRem(ctx, indexKey, id)
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// publicView strips the access token before a ticket is sent anywhere. The
// token is the only credential on an anonymous thread, so it must never travel
// back out in a response body.
func publicView(t *SupportTicket) SupportTicket {
	c := *t
	c.AccessToken = ""
	return c
}

// supportSubmitLimiters throttles ticket creation. Anonymous submissions are
// keyed by IP, which is all we have; a signed-in sender is keyed by email so a
// shared address (office NAT, mobile carrier) cannot exhaust another user's
// quota. 5 per hour with a burst of 3.
var (
	supportSubmitLimiters sync.Map
	supportSubmitMu       sync.Mutex
)

func supportSubmitLimiter(key string) *rate.Limiter {
	return getLimiter(&supportSubmitLimiters, &supportSubmitMu, key, func() *rate.Limiter {
		return rate.NewLimiter(rate.Every(12*time.Minute), 3)
	})
}

// clientKey identifies the sender for rate limiting: session email when there
// is one, otherwise the remote address. middleware.RealIP has already resolved
// RemoteAddr from X-Forwarded-For.
func clientKey(r *http.Request) string {
	if s, ok := sessionEmail(r); ok {
		return "email:" + strings.ToLower(s.Email)
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return "ip:" + host
}

// trimTo bounds a free-text field. Every one of these lands in a Redis value
// that is read back in full on each view, so length is enforced on write.
func trimTo(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// looksLikeEmail is a shape check, not validation: the address is only ever
// shown to the operator, never sent to. It exists so an obviously empty or
// junk value is refused at the door.
func looksLikeEmail(s string) bool {
	at := strings.Index(s, "@")
	if at <= 0 || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".") && !strings.ContainsAny(s, " \t\r\n")
}

// POST /api/support/tickets — open to anonymous visitors.
func createSupportTicket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	var req struct {
		Subject     string `json:"subject"`
		Body        string `json:"body"`
		Name        string `json:"name"`
		Email       string `json:"email"`
		ChannelSlug string `json:"channelSlug"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	subject := trimTo(req.Subject, maxSupportSubjectLen)
	body := trimTo(req.Body, maxSupportBodyLen)
	if subject == "" || body == "" {
		http.Error(w, "subject and body are required", http.StatusBadRequest)
		return
	}

	// A signed-in sender's identity comes from the session and is never taken
	// from the body, so a ticket cannot be attributed to someone else.
	session, authed := sessionEmail(r)
	name, email := trimTo(req.Name, maxSupportNameLen), trimTo(req.Email, maxSupportEmailLen)
	if authed {
		email = session.Email
		name = sessionDisplayName(session)
	} else {
		if !looksLikeEmail(email) {
			http.Error(w, "a valid email address is required", http.StatusBadRequest)
			return
		}
		if name == "" {
			name = email
		}
	}

	// Rated immediately before the write, never at the top: a refused request
	// must not spend quota on a message that failed validation.
	if !allowOrRetryAfter(w, supportSubmitLimiter(clientKey(r)),
		"too many requests — please try again later") {
		return
	}

	id, token := generatedRandomID(16), generatedRandomID(32)
	if id == "" || token == "" {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	now := time.Now()
	ticket := &SupportTicket{
		ID:            id,
		Subject:       subject,
		Name:          name,
		Email:         email,
		ChannelSlug:   trimTo(req.ChannelSlug, 64),
		Authenticated: authed,
		Status:        SupportStatusOpen,
		Messages: []SupportMessage{{
			Author: "user", AuthorName: name, Body: body, CreatedAt: now,
		}},
		CreatedAt:   now,
		UpdatedAt:   now,
		AccessToken: token,
	}
	if err := dbSaveSupportTicket(ctx, ticket); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	// The token is returned exactly once, here, so an anonymous sender can keep
	// the link and read the answer later.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id, "accessToken": token})
}

// authoriseTicket resolves the ticket for a requester-side request and reports
// whether this caller may see it. Two ways in: the session email matches, or
// the access token does.
func authoriseTicket(ctx context.Context, r *http.Request, id string) (*SupportTicket, bool) {
	t, err := dbGetSupportTicket(ctx, id)
	if err != nil {
		return nil, false
	}
	if s, ok := sessionEmail(r); ok && strings.EqualFold(s.Email, t.Email) {
		return t, true
	}
	token := r.URL.Query().Get("token")
	// Constant time: this is the only credential guarding an anonymous thread.
	if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(t.AccessToken)) == 1 {
		return t, true
	}
	return nil, false
}

// GET /api/support/tickets/{id}?token=... — the requester's view of a thread.
func getSupportTicket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, ok := authoriseTicket(ctx, r, chi.URLParam(r, "id"))
	if !ok {
		// Deliberately not 403: a wrong token must not confirm that the ticket
		// exists.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(publicView(t))
}

// POST /api/support/tickets/{id}/reply?token=... — the requester answers.
func replySupportTicket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, ok := authoriseTicket(ctx, r, chi.URLParam(r, "id"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if t.Status == SupportStatusClosed {
		http.Error(w, "this ticket is closed", http.StatusConflict)
		return
	}
	body, ok := decodeSupportBody(w, r)
	if !ok {
		return
	}
	if len(t.Messages) >= maxSupportMessages {
		http.Error(w, "this thread has reached its message limit", http.StatusConflict)
		return
	}
	if !allowOrRetryAfter(w, supportSubmitLimiter(clientKey(r)),
		"too many requests — please try again later") {
		return
	}

	appendSupportMessage(t, "user", t.Name, body, SupportStatusOpen)
	if err := dbSaveSupportTicket(ctx, t); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(publicView(t))
}

// GET /api/support/my-tickets — a signed-in user's own threads.
func listMySupportTickets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	s, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	tickets, err := dbListSupportTickets(ctx, supportUserIndexKey(s.Email))
	if err != nil {
		tickets = []*SupportTicket{}
	}
	out := make([]SupportTicket, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, publicView(t))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// GET /api/super-admin/support/tickets — the operator inbox.
func adminListSupportTickets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	tickets, err := dbListSupportTickets(ctx, supportTicketIndexKey)
	if err != nil {
		tickets = []*SupportTicket{}
	}
	out := make([]SupportTicket, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, publicView(t))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// POST /api/super-admin/support/tickets/{id}/reply
func adminReplySupportTicket(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, err := dbGetSupportTicket(ctx, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	body, ok := decodeSupportBody(w, r)
	if !ok {
		return
	}
	if len(t.Messages) >= maxSupportMessages {
		http.Error(w, "this thread has reached its message limit", http.StatusConflict)
		return
	}

	name := "הנהלת המערכת"
	if s, ok := sessionEmail(r); ok {
		name = sessionDisplayName(s)
	}
	// An operator reply reopens a closed ticket: answering it is a deliberate
	// act, and leaving it closed would silently discard the reply from the
	// requester's view.
	appendSupportMessage(t, "admin", name, body, SupportStatusAnswered)
	if err := dbSaveSupportTicket(ctx, t); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(publicView(t))
}

// POST /api/super-admin/support/tickets/{id}/status
func adminSetSupportTicketStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	t, err := dbGetSupportTicket(ctx, chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var req struct {
		Status SupportStatus `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil ||
		!validSupportStatus(req.Status) {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	t.Status = req.Status
	t.UpdatedAt = time.Now()
	if err := dbSaveSupportTicket(ctx, t); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(publicView(t))
}

// decodeSupportBody reads and bounds the one field every reply carries.
func decodeSupportBody(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return "", false
	}
	body := trimTo(req.Body, maxSupportBodyLen)
	if body == "" {
		http.Error(w, "message body is required", http.StatusBadRequest)
		return "", false
	}
	return body, true
}

// appendSupportMessage adds one entry and moves the ticket to status, keeping
// UpdatedAt (which is also the index score) in step.
func appendSupportMessage(t *SupportTicket, author, name, body string, status SupportStatus) {
	now := time.Now()
	t.Messages = append(t.Messages, SupportMessage{
		Author: author, AuthorName: name, Body: body, CreatedAt: now,
	})
	t.Status = status
	t.UpdatedAt = now
}
