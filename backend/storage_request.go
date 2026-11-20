package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// A channel owner asks the platform for more storage (the "אחסון" tab, and the
// banner the manage screen shows from 80 % up). The request is an ordinary
// support ticket, so it lands in the operator inbox, in Magnet's "מערכת
// הערוצים" tab and in Magnet's staff alert exactly like any other thread, and
// the owner reads the answer in their own "פנייה לתמיכה" tab. What it adds:
//
//   - the server writes the subject and the usage figures, so the operator
//     sees real numbers rather than whatever the owner typed;
//   - Topic marks the ticket, so the inbox can tag it and offer the quota field;
//   - one open request per channel: asking again while one waits returns it
//     instead of opening a second thread.

const (
	supportTopicStorageIncrease = "storage_increase"
	maxStorageRequestNoteLen    = 1000
)

type StorageIncreaseRequest struct {
	TicketID  string        `json:"ticketId"`
	Status    SupportStatus `json:"status"`
	CreatedAt time.Time     `json:"createdAt"`
}

func storageIncreaseTicketKey(slug string) string {
	return "channel:" + slug + ":storage:increase_ticket"
}

// pendingStorageIncrease returns the channel's request while the operator has
// not answered it yet. Once answered or closed the owner may ask again, and the
// pointer is dropped on the next look. A read error reads as "none": the worst
// case is a second ticket, never a blocked owner.
func pendingStorageIncrease(ctx context.Context, slug string) *StorageIncreaseRequest {
	id, err := rdb.Get(ctx, storageIncreaseTicketKey(slug)).Result()
	if err != nil || id == "" {
		return nil
	}
	t, err := dbGetSupportTicket(ctx, id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			rdb.Del(ctx, storageIncreaseTicketKey(slug))
		}
		return nil
	}
	if t.Status != SupportStatusOpen {
		return nil
	}
	return &StorageIncreaseRequest{TicketID: t.ID, Status: t.Status, CreatedAt: t.CreatedAt}
}

// formatStorageBytes renders a size for the ticket text, the way the owner's
// storage tab shows it.
func formatStorageBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.2f", float64(b)/gb), "0"), ".0") + " GB"
	case b >= mb:
		return fmt.Sprintf("%.0f MB", float64(b)/mb)
	case b >= kb:
		return fmt.Sprintf("%.0f KB", float64(b)/kb)
	}
	return fmt.Sprintf("%d B", b)
}

// storageIncreaseBody is the first message of the ticket.
func storageIncreaseBody(ch *ChannelData, slug string, info *StorageInfo, note string) string {
	name := slug
	if ch != nil && strings.TrimSpace(ch.Name) != "" {
		name = strings.TrimSpace(ch.Name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "בקשה להגדלת מכסת האחסון של הערוץ \"%s\" (%s).\n", name, slug)
	if info.QuotaBytes > 0 {
		fmt.Fprintf(&b, "בשימוש כרגע: %s מתוך %s (%.1f%%).\n",
			formatStorageBytes(info.UsedBytes), formatStorageBytes(info.QuotaBytes), info.UsedPercent)
	} else {
		fmt.Fprintf(&b, "בשימוש כרגע: %s (בלי הגבלה).\n", formatStorageBytes(info.UsedBytes))
	}
	if info.AutoCleanup {
		b.WriteString("ניקוי אוטומטי: דלוק.\n")
	} else {
		b.WriteString("ניקוי אוטומטי: כבוי.\n")
	}
	if note != "" {
		b.WriteString("\nהערה מבעלי הערוץ:\n")
		b.WriteString(note)
	}
	return strings.TrimRight(b.String(), "\n")
}

// POST /api/channel/{slug}/admin/storage/request-increase  (owner)
// Body: {"note": "..."} — optional.
// Answers {"ticketId", "existing"}: existing is true when an earlier request
// is still waiting and no new ticket was opened.
func requestStorageIncrease(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	slug := channelSlugFromCtx(r)
	session, ok := sessionEmail(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
	}
	note := trimTo(req.Note, maxStorageRequestNoteLen)

	writeResult := func(id string, existing bool) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ticketId": id, "existing": existing})
	}

	if p := pendingStorageIncrease(ctx, slug); p != nil {
		writeResult(p.TicketID, true)
		return
	}

	info, err := buildStorageInfo(ctx, slug)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	ch, _ := dbGetChannel(ctx, slug)

	// Same budget as any ticket, spent only once the request is valid.
	if !allowOrRetryAfter(w, supportSubmitLimiter(clientKey(r)),
		"too many requests — please try again later") {
		return
	}

	id := generatedRandomID(16)
	if id == "" {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	// Claim the channel's slot before writing, so two clicks (or two owners)
	// at once open one thread. A stale pointer was already cleared above only
	// when its ticket is gone; one pointing at an answered or closed ticket is
	// overwritten here.
	key := storageIncreaseTicketKey(slug)
	claimed, err := rdb.SetNX(ctx, key, id, supportTicketTTL).Result()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if !claimed {
		if p := pendingStorageIncrease(ctx, slug); p != nil {
			writeResult(p.TicketID, true)
			return
		}
		if err := rdb.Set(ctx, key, id, supportTicketTTL).Err(); err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
	}

	name := sessionDisplayName(session)
	now := time.Now()
	subject := "בקשת הגדלת אחסון"
	if ch != nil && strings.TrimSpace(ch.Name) != "" {
		subject += ": " + strings.TrimSpace(ch.Name)
	}
	ticket := &SupportTicket{
		ID:            id,
		Subject:       trimTo(subject, maxSupportSubjectLen),
		Name:          name,
		Email:         session.Email,
		ChannelSlug:   slug,
		Topic:         supportTopicStorageIncrease,
		Authenticated: true,
		Status:        SupportStatusOpen,
		Messages: []SupportMessage{{
			Author: "user", AuthorName: name,
			Body:      trimTo(storageIncreaseBody(ch, slug, info, note), maxSupportBodyLen),
			CreatedAt: now,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := dbSaveSupportTicket(ctx, ticket); err != nil {
		rdb.Del(ctx, key)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	log.Printf("requestStorageIncrease: %s: ticket %s (%.1f%% used)\n", slug, id, info.UsedPercent)

	created := *ticket
	dispatchMagnetMail(func(mctx context.Context) (*magnetMailEvent, bool) {
		return supportActivityEvent(&created, 0, ticketMailChannel(mctx, &created))
	})

	writeResult(id, false)
}
