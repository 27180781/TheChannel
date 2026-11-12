package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// support_activity: a requester wrote on the site — a new thread, or a
// follow-up that reopened one — and Magnet's staff should know.
//
// It travels over the MAGNET_MAIL_URL pipe (same secret, same retries, same
// idempotency) because Magnet already trusts that pipe for this one host, and
// this way no new variable is needed for the alert. Unlike the other two
// events it mails nobody outside Magnet: Magnet's channels-mail alerts its
// admins and the tab's badge follows the open count. `to` is empty.
//
// Mail to the channels mailbox that opens or extends a thread is NOT reported
// here: it passed through Magnet on its way in, so Magnet has it already.

const maxSupportActivityPreview = 500

type magnetSupportActivity struct {
	Kind           string  `json:"kind"` // "created" | "follow_up"
	TicketID       string  `json:"ticket_id"`
	Subject        string  `json:"subject"`
	RequesterName  *string `json:"requester_name"`
	RequesterEmail string  `json:"requester_email"`
	Authenticated  bool    `json:"authenticated"`
	ChannelSlug    string  `json:"channel_slug"`
	Preview        string  `json:"preview"`
	MessageCount   int     `json:"message_count"`
}

// supportPreviewText is the start of a message on one line, for the alert only.
func supportPreviewText(s string) string {
	// strings.Fields splits on every Unicode space, newlines included; the
	// remaining controls are dropped, as oneLine does (without its 200 cap).
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, strings.Join(strings.Fields(s), " "))
	if utf8.RuneCountInString(s) <= maxSupportActivityPreview {
		return s
	}
	r := []rune(s)
	return string(r[:maxSupportActivityPreview]) + "…"
}

// supportActivityEvent reports the requester message at msgIndex. ch is the
// channel the ticket names, already filtered by ticketMailChannel (a private
// channel's name is not passed on for an anonymous requester), or nil.
func supportActivityEvent(t *SupportTicket, msgIndex int, ch *ChannelData) (*magnetMailEvent, bool) {
	if t == nil || msgIndex < 0 || msgIndex >= len(t.Messages) {
		return nil, false
	}
	m := t.Messages[msgIndex]
	if m.Author != "user" {
		return nil, false
	}
	kind := "follow_up"
	if msgIndex == 0 {
		kind = "created"
	}
	email := ""
	if looksLikeEmail(t.Email) {
		email = t.Email
	}
	base := publicBaseURL()
	ev := &magnetMailEvent{
		Event:          magnetMailEventSupportActivity,
		IdempotencyKey: capKey(fmt.Sprintf("support_activity:%s:%d", t.ID, msgIndex)),
		ContactURL:     supportContactURL(base, ""),
		Support: &magnetSupportActivity{
			Kind:           kind,
			TicketID:       t.ID,
			Subject:        oneLine(t.Subject),
			RequesterName:  mailToName(t.Name, t.Email),
			RequesterEmail: email,
			Authenticated:  t.Authenticated,
			Preview:        supportPreviewText(m.Body),
			MessageCount:   len(t.Messages),
		},
	}
	if ch != nil {
		ev.Channel = mailChannel(base, ch)
		ev.Support.ChannelSlug = ch.Slug
	}
	return ev, true
}
