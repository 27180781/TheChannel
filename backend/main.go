package main

import (
	"bytes"
	"encoding/gob"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"

	"github.com/boj/redistore"
	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
)

var rootStaticFolder = os.Getenv("ROOT_STATIC_FOLDER")

func main() {
	gob.Register(Session{})
	initializePrivilegeUsers()
	go statLogger()

	var err error
	store, err = redistore.NewRediStore(10, redisType, redisAddr, "", redisPass, []byte(secretKey))
	if err != nil {
		panic(err)
	}
	store.SetMaxAge(60 * 60 * 24 * 30)
	store.Options.HttpOnly = true
	defer store.Close()

	r := chi.NewRouter()
	r.Use(middleware.Logger)

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
		r.Get("/magnet/config", getGlobalMagnetConfig)
		r.Post("/magnet/config", setGlobalMagnetConfig)
		r.Get("/magnet/stats", getMagnetStats)
		r.Post("/statistics/reset", resetStatistics)
	})

	// Per-channel API import (with API key, no channel middleware needed - slug from URL)
	r.Post("/api/channel/{slug}/import/post", addNewPost)

	// Per-channel routes
	r.Route("/api/channel/{slug}", func(r chi.Router) {
		r.Use(channelMiddleware)
		r.Use(channelIfRequireAuth)

		r.Get("/info", getChannelInfo)
		r.Get("/messages", getMessages)
		r.Get("/events", getEvents)
		r.Get("/files/{fileid}", serveFile)
		r.Get("/emojis/list", getEmojisList)
		r.Get("/notifications-config", getNotificationsConfig)
		r.Get("/ads/settings", getAdsSettings)
		r.Get("/ads/magnet", getMagnetAdsSettings)

		r.Group(func(r chi.Router) {
			r.Use(checkLogin)
			r.Post("/notifications-subscribe", subscribeNotifications)
			r.Post("/reactions/set-reactions", setReactions)
			r.Post("/messages/report", reportMessage)
			r.Get("/user-info", getUserInfo)

			r.Route("/admin", func(r chi.Router) {
				// Writer level
				r.Post("/new", protectedWithChannelRole(RoleWriter, addMessage))
				r.Post("/edit-message", protectedWithChannelRole(RoleWriter, updateMessage))
				r.Get("/delete-message/{id}", protectedWithChannelRole(RoleWriter, deleteMessage))
				r.Post("/upload", protectedWithChannelRole(RoleWriter, uploadFile))
				r.Get("/scheduled-messages/get", protectedWithChannelRole(RoleWriter, getScheduledMessages))
				r.Post("/scheduled-messages/update", protectedWithChannelRole(RoleWriter, updateScheduledMessages))

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
			})
		})
	})

	if settingConfig != nil && settingConfig.RootStaticFolder != "" {
		r.Handle("/assets/*", http.StripPrefix("/assets/", http.FileServer(http.Dir(settingConfig.RootStaticFolder))))
		r.NotFound(serveSpaFile)
	}

	go func() {
		log.Fatal(http.ListenAndServe("localhost:6060", nil))
	}()

	if err := http.ListenAndServe(":"+os.Getenv("SERVER_PORT"), r); err != nil {
		log.Fatal(err)
	}
}

func serveSpaFile(w http.ResponseWriter, r *http.Request) {
	if settingConfig == nil || settingConfig.RootStaticFolder == "" {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	htmlPath := filepath.Join(settingConfig.RootStaticFolder, "index.html")
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	if settingConfig.CustomTitle != "" {
		content = bytes.ReplaceAll(content, []byte("<title></title>"), []byte(settingConfig.CustomTitle))
	}

	if settingConfig.AnalyticsHead != "" {
		content = bytes.Replace(content, []byte("</head>"), []byte(settingConfig.AnalyticsHead+"</head>"), 1)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}
