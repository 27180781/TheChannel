package main

import (
	"bytes"
	"encoding/gob"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/boj/redistore"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

var rootStaticFolder = os.Getenv("ROOT_STATIC_FOLDER")

func main() {
	gob.Register(Session{})
	initR2()
	if err := initializePrivilegeUsers(); err != nil {
		panic(err)
	}

	// The session store is built before the migrations run: a misconfiguration
	// here is fatal, and failing after a one-shot migration has been marked
	// applied would consume it without ever serving a request.
	var err error
	store, err = redistore.NewRediStore(10, redisType, redisAddr, "", redisPass, []byte(secretKey))
	if err != nil {
		log.Fatalf("Session store init failed (REDIS_PROTOCOL=%q, REDIS_ADDR=%q): %v", redisType, redisAddr, err)
	}
	store.SetMaxAge(60 * 60 * 24 * 30)
	store.Options.HttpOnly = true
	// Secure by default; set COOKIE_INSECURE=1 only for local plain-HTTP dev.
	store.Options.Secure = os.Getenv("COOKIE_INSECURE") != "1"
	// Lax (not Strict) so the cookie still rides along on the Google OAuth
	// redirect back to the site and on inbound links to authenticated views.
	store.Options.SameSite = http.SameSiteLaxMode
	defer store.Close()

	// Data migrations run to completion before the listener starts, so a request
	// never observes a half-migrated state. A failure is logged and retried on
	// the next boot rather than taking the server down.
	migCtx, migCancel := migrationContext()
	runMigrations(migCtx)
	migCancel()

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	// The shipped deployment fronts the backend with Caddy (docker-compose
	// exposes only the proxy), so RemoteAddr is the proxy's container IP for
	// every request. RealIP restores the client address from X-Forwarded-For so
	// per-IP rate limiting works; the backend port must therefore never be
	// exposed directly, since RealIP trusts the header.
	r.Use(middleware.RealIP)
	r.Use(limitRequestBody)

	// Auth routes
	r.Get("/auth/google", getGoogleAuthValues)
	r.Post("/auth/login", login)
	r.Post("/auth/logout", logout)

	// Global assets
	r.Get("/assets/favicon.ico", getFavicon)
	r.Get("/favicon.ico", getFavicon)
	r.Get("/firebase-messaging-sw.js", getFirebaseMessagingSW)

	// User info (global, requires login)
	r.Group(func(r chi.Router) {
		r.Use(checkLogin)
		r.Get("/api/user-info", getUserInfo)
		// Self-service channel creation: the owner is taken from the session,
		// never from the body, so nobody can claim a slug for someone else.
		r.Post("/api/channels/create", createChannelSelfService)
		r.Get("/api/channels/slug-available", checkSlugAvailable)
	})

	// Super admin routes
	r.Route("/api/super-admin", func(r chi.Router) {
		r.Use(checkLogin)
		r.Use(requireSuperAdmin)

		r.Get("/channels", listChannels)
		r.Post("/channels/create", createChannel)
		r.Get("/channels/{slug}", getSuperAdminChannel)
		r.Delete("/channels/{slug}", deleteChannel)
		r.Put("/channels/{slug}/features", updateChannelFeatures)
		r.Get("/channels/{slug}/users", superAdminGetChannelUsers)
		r.Post("/channels/{slug}/users", superAdminSetChannelUsers)
		r.Get("/users/list", getPrivilegeUsersList)
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
		r.Post("/support/tickets/{id}/reply", adminReplySupportTicket)
		r.Post("/support/tickets/{id}/status", adminSetSupportTicketStatus)
		r.Get("/channel-requests", listChannelRequests)
		r.Post("/channel-requests/{id}/approve", approveChannelRequest)
		r.Post("/channel-requests/{id}/reject", rejectChannelRequest)
	})

	// Support tickets. Creation and the thread view are deliberately outside
	// checkLogin: a visitor who cannot sign in (or has not yet) is exactly the
	// person who most needs to reach the operator. An anonymous thread is
	// guarded by the unguessable token minted at creation.
	r.Post("/api/support/tickets", createSupportTicket)
	r.Get("/api/support/tickets/{id}", getSupportTicket)
	r.Post("/api/support/tickets/{id}/reply", replySupportTicket)
	r.With(checkLogin).Get("/api/support/my-tickets", listMySupportTickets)

	// Per-channel API import (with API key, no channel middleware needed - slug from URL)
	r.Post("/api/channel/{slug}/import/post", addNewPost)

	// Per-channel routes
	r.Route("/api/channel/{slug}", func(r chi.Router) {
		r.Use(channelMiddleware)
		r.Use(channelIfRequireAuth)

		r.Get("/info", getChannelInfo)
		r.Get("/messages", getMessages)
		r.Get("/events", getEvents)
		r.With(channelIfRequireAuthFiles).Get("/files/{fileid}", serveFile)
		r.Get("/emojis/list", getEmojisList)
		r.Get("/notifications-config", getNotificationsConfig)
		r.Get("/ads/settings", getAdsSettings)
		r.Get("/ads/magnet", getMagnetAdsSettings)

		r.Group(func(r chi.Router) {
			r.Use(checkLogin)
			r.Post("/notifications-subscribe", subscribeNotifications)
			r.Post("/reactions/set-reactions", requireFeature(func(f *ChannelFeatures) bool { return f.Reactions }, setReactions))
			r.Post("/messages/report", requireFeature(func(f *ChannelFeatures) bool { return f.Reports }, reportMessage))
			r.Get("/user-info", getUserInfo)

			r.Route("/admin", func(r chi.Router) {
				// Writer level
				r.Post("/new", protectedWithChannelRole(RoleWriter, addMessage))
				r.Post("/edit-message", protectedWithChannelRole(RoleWriter, updateMessage))
				// DELETE only. The GET alias that used to sit here was a CSRF
				// hole: the session cookie is SameSite=Lax, which deliberately
				// still rides along on a top-level GET navigation, so merely
				// visiting a link like
				// /api/channel/<slug>/admin/delete-message/42 was enough to
				// delete a writer's message from any site on the web. Lax
				// withholds the cookie from cross-site DELETE, so the verb is
				// the whole defence. The only client (admin.service.ts) has
				// always used DELETE.
				r.Delete("/delete-message/{id}", protectedWithChannelRole(RoleWriter, deleteMessage))
				r.Post("/upload", protectedWithChannelRole(RoleWriter, requireFeature(func(f *ChannelFeatures) bool { return f.FileUploads }, uploadRateLimit(uploadFile))))
				r.Get("/scheduled-messages/get", protectedWithChannelRole(RoleWriter, requireFeature(func(f *ChannelFeatures) bool { return f.ScheduledMessages }, getScheduledMessages)))
				r.Post("/scheduled-messages/update", protectedWithChannelRole(RoleWriter, requireFeature(func(f *ChannelFeatures) bool { return f.ScheduledMessages }, updateScheduledMessages)))

				// Moderator level
				r.Post("/edit-channel-info", protectedWithChannelRole(RoleModerator, editChannelInfo))
				r.Get("/statistics", protectedWithChannelRole(RoleModerator, getStatistics))
				r.Post("/set-emojis", protectedWithChannelRole(RoleModerator, setEmojis))
				r.Get("/reports/get", protectedWithChannelRole(RoleModerator, getReports))
				r.Post("/reports/set", protectedWithChannelRole(RoleModerator, setReports))

				// Owner level
				r.Get("/settings/get", protectedWithChannelRole(RoleOwner, getSettings))
				r.Post("/settings/set", protectedWithChannelRole(RoleOwner, setSettings))
				r.Get("/users/get", protectedWithChannelRole(RoleOwner, getChannelUsers))
				r.Post("/users/set", protectedWithChannelRole(RoleOwner, setChannelUsers))
				r.Get("/storage", protectedWithChannelRole(RoleOwner, getChannelStorageInfo))
				r.Post("/storage/auto-cleanup", protectedWithChannelRole(RoleOwner, setChannelAutoCleanup))
			})
		})
	})

	if cfg := getGlobalConfig(); cfg != nil && cfg.RootStaticFolder != "" {
		r.Handle("/assets/*", staticCacheHeaders(http.StripPrefix("/assets/", http.FileServer(http.Dir(cfg.RootStaticFolder)))))
		r.NotFound(serveSpaFile)
	}

	// The pprof listener is a debugging aid, not part of the service. Binding it
	// must never be able to take the real server down, so the error is logged
	// instead of fatal, and it only starts when explicitly asked for.
	if pprofAddr := os.Getenv("PPROF_ADDR"); pprofAddr != "" {
		go func() {
			if err := http.ListenAndServe(pprofAddr, nil); err != nil {
				log.Printf("pprof listener stopped: %v\n", err)
			}
		}()
	}

	if err := http.ListenAndServe(":"+os.Getenv("SERVER_PORT"), r); err != nil {
		log.Fatal(err)
	}
}

// maxJSONRequestBody caps any non-multipart request body. Handlers decode
// straight into memory with json.NewDecoder(r.Body), which reads as much as the
// client sends: without a cap, a single authenticated writer could post a
// multi-gigabyte body to /admin/new and have it buffered, stored verbatim and
// then fanned out to every SSE viewer of the channel.
//
// 2 MiB is far above any legitimate payload here — the largest is a scheduled
// message list, and message text is separately capped at 100k — while bounding
// what one request can allocate.
const maxJSONRequestBody = 2 << 20

// limitRequestBody applies that cap to every route, so a handler added later
// cannot forget it. File uploads are exempt: they are multipart and enforce
// their own, much larger, per-channel size limit in uploadFile.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}

func serveSpaFile(w http.ResponseWriter, r *http.Request) {
	cfg := getGlobalConfig()
	if cfg == nil || cfg.RootStaticFolder == "" {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	htmlPath := filepath.Join(cfg.RootStaticFolder, "index.html")
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	if cfg.CustomTitle != "" {
		content = bytes.ReplaceAll(content, []byte("<title></title>"), []byte(cfg.CustomTitle))
	}

	if cfg.AnalyticsHead != "" {
		content = bytes.Replace(content, []byte("</head>"), []byte(cfg.AnalyticsHead+"</head>"), 1)
	}

	// index.html must never be cached without revalidation. Every deploy gives
	// the bundles new content-hashed names, and this file is the only thing
	// that says which names are current. Cached by a browser or an upstream
	// proxy, it outlives the bundles it points at: the next visitor with a cold
	// cache gets a stale document whose /assets/main-<hash>.js is gone, the
	// script 404s, and app-root is left empty — a white page with no error the
	// application could report, because the application never started.
	//
	// It is also rewritten per-deployment below (title, analytics), so a shared
	// cache holding it would serve another operator's markup.
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

// hashedAssetRe matches the content-hashed filenames the frontend build emits
// (main-EE7HE3OC.js, styles-ZXVD3ZCM.css, media/NotoColorEmoji-HW4CGOE5.ttf).
// The hash changes whenever the contents do, so these are safe to cache
// forever; anything else under /assets (favicon.ico and friends) is not.
var hashedAssetRe = regexp.MustCompile(`-[A-Z0-9]{8}\.[a-zA-Z0-9]+$`)

// staticCacheHeaders pairs the immutable bundles with the no-cache index.html
// above: the hashed files are what make it safe for index.html to be
// revalidated on every visit, since only the small document is ever refetched.
func staticCacheHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hashedAssetRe.MatchString(r.URL.Path) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// Un-hashed and therefore replaceable in place: revalidate rather
			// than pin an old copy for a year.
			w.Header().Set("Cache-Control", "public, max-age=300")
		}
		next.ServeHTTP(w, r)
	})
}
