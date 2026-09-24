package main

import (
	"encoding/json"
	"strings"
)

// operatorName is the only name anyone else is ever shown for a platform
// operator (a super admin): on a support reply, a channel post, a report.
//
// The operator's own identity — public name, Google name and account id, email
// — must never reach another person, and that includes JSON fields and webhook
// payloads the page does not render. Records are written with this label, and
// the read paths below re-label records written before that was the rule.
const operatorName = "ניהול"

// operatorAuthorId stands in for an operator's Google account id wherever a
// record would otherwise carry it. Deliberately not "" or "0": canModifyMessage
// treats those as system posts that any writer may edit. Operators lose nothing
// by not matching it, since hasChannelRole already lets them edit anything.
const operatorAuthorId = "operator"

// operatorSet is a snapshot of who the operators are, for recognising their
// identity in records written before operators were anonymised.
type operatorSet struct {
	ids    map[string]struct{}
	emails map[string]struct{}
}

// currentOperators snapshots the super admins from privilegesUsers. The map
// holds privileged users only, so a range is cheap; callers still take one
// snapshot per request or event rather than one per record.
//
// An operator's id is their Google account id, filled in on first login. One
// who has never logged in has no id and so cannot have written anything yet.
func currentOperators() operatorSet {
	ops := operatorSet{ids: map[string]struct{}{}, emails: map[string]struct{}{}}
	privilegesUsers.Range(func(_, v any) bool {
		u, ok := v.(User)
		if !ok || u.GlobalRole != RoleSuperAdmin {
			return true
		}
		if u.ID != "" {
			ops.ids[u.ID] = struct{}{}
		}
		if u.Email != "" {
			ops.emails[strings.ToLower(u.Email)] = struct{}{}
		}
		return true
	})
	return ops
}

func (o operatorSet) hasID(id string) bool {
	if id == "" {
		return false
	}
	_, ok := o.ids[id]
	return ok
}

func (o operatorSet) hasEmail(email string) bool {
	if email == "" {
		return false
	}
	_, ok := o.emails[strings.ToLower(email)]
	return ok
}

// anonymiseOperatorAuthor re-labels a message written by an operator and
// reports whether it changed anything. Other authors are left alone.
func (o operatorSet) anonymiseOperatorAuthor(m *Message) bool {
	if !o.hasID(m.AuthorId) {
		return false
	}
	m.Author, m.AuthorId = operatorName, operatorAuthorId
	return true
}

// anonymiseOperatorReport re-labels a report an operator filed. The email is
// cleared rather than replaced: there is no stand-in address to show.
func (o operatorSet) anonymiseOperatorReport(rep *Report) {
	if !o.hasID(rep.ReporterID) && !o.hasEmail(rep.ReportedEmail) {
		return
	}
	rep.ReporterID, rep.ReportedEmail, rep.ReporterName = operatorAuthorId, "", operatorName
}

// anonymiseOperatorEvent re-labels an operator's message inside an SSE event
// payload. The stream keeps ~1000 entries per channel and replays them to a
// reconnecting viewer, so events published before operators were anonymised
// are still served. Anything that is not an operator's message passes through
// byte for byte, and a payload that cannot contain an operator id is not even
// parsed.
func (o operatorSet) anonymiseOperatorEvent(data string) string {
	mentioned := false
	for id := range o.ids {
		if strings.Contains(data, id) {
			mentioned = true
			break
		}
	}
	if !mentioned {
		return data
	}
	var pm PushMessage
	if err := json.Unmarshal([]byte(data), &pm); err != nil {
		return data
	}
	if !o.anonymiseOperatorAuthor(&pm.M) {
		return data
	}
	out, err := json.Marshal(pm)
	if err != nil {
		return data
	}
	return string(out)
}
