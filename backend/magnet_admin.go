package main

import (
	"context"
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/go-chi/chi"
)

// The super-admin panel, run from inside Magnet.
//
// Magnet's admin area has a "מערכת הערוצים" tab that shows the support inbox
// and every super-admin screen. The data stays here: Magnet's Edge Function
// (channels-admin) checks that the caller is a signed-in Magnet admin and then
// calls the very same handlers the panel uses, server to server:
//
//	<METHOD> /api/integrations/magnet/admin/<super-admin path>
//	x-magnet-admin-secret: $MAGNET_ADMIN_SECRET
//	x-magnet-operator:     <who in Magnet pressed the button, for the log>
//
// The secret is deliberately its own variable, not MAGNET_MAIL_SECRET: that one
// only lets Magnet hand over mail, and a leak of it must not become a key to
// the whole platform. Unset means the door does not exist (404), the same
// answer as any unknown path, so a probe learns nothing.
//
// The caller acts as a super admin and nothing more: isSuperAdmin is true for
// the request, every other identity lookup (sessionEmail, sessionUser) still
// finds no session. A reply written this way is signed "ניהול" like any
// operator reply, and answeringOwnTicket sees no cookie, so the requester is
// always mailed.

const (
	magnetAdminSecretHeader   = "x-magnet-admin-secret"
	magnetAdminOperatorHeader = "x-magnet-operator"
	maxMagnetOperatorLen      = 200
)

var (
	magnetAdminSecret         = strings.TrimSpace(os.Getenv("MAGNET_ADMIN_SECRET"))
	magnetAdminOffLogged      sync.Once
	magnetAdminOperatorCtxKey = &struct{ name string }{"magnet-operator"}
)

// magnetOperator is who in Magnet made this request, or false when the request
// did not come through the Magnet admin door.
func magnetOperator(r *http.Request) (string, bool) {
	v, ok := r.Context().Value(magnetAdminOperatorCtxKey).(string)
	return v, ok && v != ""
}

// cleanOperator keeps the operator label printable and bounded: it goes into
// the log line and nowhere else.
func cleanOperator(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if len(s) > maxMagnetOperatorLen {
		s = s[:maxMagnetOperatorLen]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
	}
	return s
}

// requireMagnetAdmin admits a request that carries MAGNET_ADMIN_SECRET and
// names its operator. Missing configuration and a wrong secret both answer
// 404, so the route's existence is not advertised.
func requireMagnetAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if magnetAdminSecret == "" {
			magnetAdminOffLogged.Do(func() {
				log.Println("magnet admin: MAGNET_ADMIN_SECRET is not set — the Magnet admin tab is off")
			})
			http.NotFound(w, r)
			return
		}
		got := r.Header.Get(magnetAdminSecretHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(magnetAdminSecret)) != 1 {
			http.NotFound(w, r)
			return
		}
		op := cleanOperator(r.Header.Get(magnetAdminOperatorHeader))
		if op == "" {
			http.Error(w, "operator is required", http.StatusBadRequest)
			return
		}
		// Every call is a super-admin action; the log line is its audit trail.
		// The path never carries a secret (ticket tokens travel in a header).
		log.Printf("magnet admin: %s %s by %q", r.Method, r.URL.Path, op)
		ctx := context.WithValue(r.Context(), magnetAdminOperatorCtxKey, op)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// mountSuperAdminRoutes is the one list of super-admin endpoints. The panel
// mounts it behind checkLogin + requireSuperAdmin, Magnet behind
// requireMagnetAdmin, so the two can never drift apart.
func mountSuperAdminRoutes(r chi.Router) {
	r.Get("/channels", listChannels)
	r.Post("/channels/create", createChannel)
	r.Get("/channels/{slug}", getSuperAdminChannel)
	r.Delete("/channels/{slug}", deleteChannel)
	r.Put("/channels/{slug}/features", updateChannelFeatures)
	r.Put("/channels/{slug}/seo", superAdminSetChannelSeo)
	r.Get("/channels/{slug}/users", superAdminGetChannelUsers)
	r.Post("/channels/{slug}/users", superAdminSetChannelUsers)
	r.Get("/users/list", getPrivilegeUsersList)
	r.Get("/consents.csv", exportOwnerConsents)
	r.Get("/signins.csv", exportSignIns)
	r.Post("/users/set", setPrivilegeUsers)
	r.Get("/global-settings/get", getGlobalSettings)
	r.Post("/global-settings/set", setGlobalSettings)
	r.Get("/ads/config", getGlobalAdsConfig)
	r.Post("/ads/config", setGlobalAdsConfig)
	r.Get("/magnet/config", getGlobalMagnetConfig)
	r.Post("/magnet/config", setGlobalMagnetConfig)
	r.Get("/magnet/stats", getMagnetStats)
	r.Get("/storage/config", getSuperAdminStorageConfig)
	r.Post("/storage/config", setSuperAdminStorageConfig)
	r.Get("/channels/{slug}/storage", getSuperAdminChannelStorage)
	r.Put("/channels/{slug}/storage", setSuperAdminChannelStorage)
	r.Post("/statistics/reset", resetStatistics)
	r.Get("/support/tickets", adminListSupportTickets)
	r.Get("/support/summary", adminSupportSummary)
	r.Post("/support/tickets/{id}/reply", adminReplySupportTicket)
	r.Post("/support/tickets/{id}/status", adminSetSupportTicketStatus)
}
