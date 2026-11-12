package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi"
)

// magnetAdminRouter mounts a probe behind requireMagnetAdmin that reports what
// a handler sees: whether it counts as a super admin, and the operator label.
func magnetAdminRouter() http.Handler {
	r := chi.NewRouter()
	r.Route("/api/integrations/magnet/admin", func(r chi.Router) {
		r.Use(requireMagnetAdmin)
		r.Get("/probe", func(w http.ResponseWriter, r *http.Request) {
			op, _ := magnetOperator(r)
			json.NewEncoder(w).Encode(map[string]any{"super": isSuperAdmin(r), "op": op})
		})
	})
	return r
}

func withMagnetAdminSecret(t *testing.T, v string) {
	t.Helper()
	old := magnetAdminSecret
	magnetAdminSecret = v
	t.Cleanup(func() { magnetAdminSecret = old })
}

func magnetAdminCall(h http.Handler, secret, op string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/magnet/admin/probe", nil)
	if secret != "" {
		req.Header.Set(magnetAdminSecretHeader, secret)
	}
	if op != "" {
		req.Header.Set(magnetAdminOperatorHeader, op)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Unset, the door does not exist: even the right-looking request is a 404.
func TestMagnetAdminOffWithoutSecret(t *testing.T) {
	withMagnetAdminSecret(t, "")
	if rec := magnetAdminCall(magnetAdminRouter(), "anything", "נסים"); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// A wrong or missing secret answers exactly like an unknown path.
func TestMagnetAdminRefusesWrongSecret(t *testing.T) {
	withMagnetAdminSecret(t, "s3cret-value")
	h := magnetAdminRouter()
	for _, s := range []string{"", "s3cret-valuf", "s3cret-value-and-more", "S3CRET-VALUE"} {
		if rec := magnetAdminCall(h, s, "נסים"); rec.Code != http.StatusNotFound {
			t.Fatalf("secret %q: status %d, want 404", s, rec.Code)
		}
	}
}

// Every call must say who in Magnet made it; that label is the audit trail.
func TestMagnetAdminRequiresOperator(t *testing.T) {
	withMagnetAdminSecret(t, "s3cret-value")
	h := magnetAdminRouter()
	for _, op := range []string{"", "   ", "\x01\x02"} {
		if rec := magnetAdminCall(h, "s3cret-value", op); rec.Code != http.StatusBadRequest {
			t.Fatalf("operator %q: status %d, want 400", op, rec.Code)
		}
	}
}

func TestMagnetAdminActsAsSuperAdmin(t *testing.T) {
	withMagnetAdminSecret(t, "s3cret-value")
	rec := magnetAdminCall(magnetAdminRouter(), "s3cret-value", "  נסים\x07 (admin)  ")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Super bool   `json:"super"`
		Op    string `json:"op"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Super {
		t.Fatal("a Magnet admin request must count as a super admin")
	}
	if got.Op != "נסים (admin)" {
		t.Fatalf("operator %q, want control characters stripped", got.Op)
	}
}

// Outside the door nothing changes: no operator in context, no super admin.
func TestMagnetOperatorAbsentOnOrdinaryRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, ok := magnetOperator(req); ok {
		t.Fatal("an ordinary request must carry no Magnet operator")
	}
}

// Magnet sends the Hebrew label percent-encoded; the log gets it readable.
func TestMagnetAdminDecodesOperator(t *testing.T) {
	withMagnetAdminSecret(t, "s3cret-value")
	rec := magnetAdminCall(magnetAdminRouter(), "s3cret-value", "%D7%A0%D7%A1%D7%99%D7%9D%20%C2%B7%20a%40b.co")
	var got struct {
		Op string `json:"op"`
	}
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Op != "נסים · a@b.co" {
		t.Fatalf("operator %q", got.Op)
	}
}

func TestCleanOperatorKeepsValidUTF8WhenCut(t *testing.T) {
	got := cleanOperator(strings.Repeat("א", 150)) // 300 bytes
	if len(got) > maxMagnetOperatorLen {
		t.Fatalf("len %d over the cap", len(got))
	}
	if !strings.HasPrefix(strings.Repeat("א", 150), got) || len(got)%2 != 0 {
		t.Fatalf("cut through a character: %q", got)
	}
}

func activityTicket() *SupportTicket {
	now := time.Now()
	return &SupportTicket{
		ID: "t1", Subject: "שאלה\nעל ערוץ", Name: "דנה", Email: "dana@example.com",
		Status: SupportStatusOpen,
		Messages: []SupportMessage{
			{Author: "user", Body: "הודעה ראשונה", CreatedAt: now},
			{Author: "admin", AuthorName: operatorName, Body: "תשובה", CreatedAt: now},
			{Author: "user", Body: "  עוד\n\nשאלה  ", CreatedAt: now},
		},
	}
}

func TestSupportActivityEventCreatedAndFollowUp(t *testing.T) {
	tk := activityTicket()

	ev, ok := supportActivityEvent(tk, 0, nil)
	if !ok || ev.Event != magnetMailEventSupportActivity || ev.Support.Kind != "created" {
		t.Fatalf("first message: %+v %v", ev, ok)
	}
	if ev.To != "" {
		t.Fatal("support_activity mails nobody outside Magnet; to must be empty")
	}
	if ev.IdempotencyKey != "support_activity:t1:0" || ev.Support.Subject != "שאלה על ערוץ" {
		t.Fatalf("key/subject: %q %q", ev.IdempotencyKey, ev.Support.Subject)
	}
	if ev.Support.RequesterEmail != "dana@example.com" || ev.Support.RequesterName == nil || *ev.Support.RequesterName != "דנה" {
		t.Fatalf("requester: %+v", ev.Support)
	}

	ev, ok = supportActivityEvent(tk, 2, nil)
	if !ok || ev.Support.Kind != "follow_up" || ev.Support.Preview != "עוד שאלה" || ev.Support.MessageCount != 3 {
		t.Fatalf("follow-up: %+v %v", ev.Support, ok)
	}

	// The operator's own reply is not activity to alert about.
	if _, ok := supportActivityEvent(tk, 1, nil); ok {
		t.Fatal("an admin message must not raise support_activity")
	}
	if _, ok := supportActivityEvent(tk, 9, nil); ok {
		t.Fatal("out of range index must send nothing")
	}
}

func TestSupportActivityPreviewIsBounded(t *testing.T) {
	got := supportPreviewText(strings.Repeat("ש", maxSupportActivityPreview+50))
	if n := len([]rune(got)); n != maxSupportActivityPreview+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("preview runes %d", n)
	}
}

// The badge count follows tickets as they open and get answered.
func TestAdminSupportSummaryCountsOpen(t *testing.T) {
	ctx := supportCtx(t)
	read := func() (open int, last *time.Time) {
		rec := httptest.NewRecorder()
		adminSupportSummary(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
		var s struct {
			Open       int        `json:"open"`
			LastOpenAt *time.Time `json:"lastOpenAt"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&s); err != nil {
			t.Fatal(err)
		}
		return s.Open, s.LastOpenAt
	}
	before, _ := read()
	newTestTicket(t, ctx, "summary-a", "summary-a@example.com")
	newTestTicket(t, ctx, "summary-b", "summary-b@example.com")
	if n, last := read(); n != before+2 || last == nil {
		t.Fatalf("open %d (before %d), last %v", n, before, last)
	}
	if _, err := dbUpdateSupportTicket(ctx, "summary-a", func(cur *SupportTicket) error {
		appendSupportMessage(cur, "admin", operatorName, "ok", SupportStatusAnswered)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n, _ := read(); n != before+1 {
		t.Fatalf("after answering one: open %d, want %d", n, before+1)
	}
}
