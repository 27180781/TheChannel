package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type GlobalRole string
type ChannelRole string

const (
	RoleSuperAdmin GlobalRole = "super_admin"
)

const (
	RoleOwner     ChannelRole = "owner"
	RoleModerator ChannelRole = "moderator"
	RoleWriter    ChannelRole = "writer"
)

var channelRoleLevels = map[ChannelRole]int{
	RoleWriter:    1,
	RoleModerator: 2,
	RoleOwner:     3,
}

var privilegesUsers sync.Map
var superAdminEmails []string

func initializePrivilegeUsers() {
	superAdminEmails = strings.Split(os.Getenv("ADMIN_USERS"), ",")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	privilegesUsers.Clear()
	users, err := dbGetUsersList(ctx)
	if err != nil && err != redis.Nil {
		panic("Failed to get users list: " + err.Error())
	}

	emailToIdx := make(map[string]int)
	for i, u := range users {
		emailToIdx[u.Email] = i
	}

	for _, email := range superAdminEmails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}
		if i, exists := emailToIdx[email]; exists {
			users[i].GlobalRole = RoleSuperAdmin
		} else {
			users = append(users, User{
				Email:      email,
				GlobalRole: RoleSuperAdmin,
			})
		}
	}

	for _, user := range users {
		privilegesUsers.Store(user.Email, user)
	}

	if err := dbSetUsersList(ctx, users); err != nil {
		panic("Failed to set users list: " + err.Error())
	}
}

func isSuperAdmin(r *http.Request) bool {
	session, _ := store.Get(r, cookieName)
	s, ok := session.Values["user"].(Session)
	if !ok {
		return false
	}
	return s.GlobalRole == RoleSuperAdmin
}

func hasChannelRole(r *http.Request, slug string, minRole ChannelRole) bool {
	if isSuperAdmin(r) {
		return true
	}
	session, _ := store.Get(r, cookieName)
	s, ok := session.Values["user"].(Session)
	if !ok {
		return false
	}
	if s.ChannelRoles == nil {
		return false
	}
	role, exists := s.ChannelRoles[slug]
	if !exists {
		return false
	}
	return channelRoleLevels[role] >= channelRoleLevels[minRole]
}

func requireSuperAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isSuperAdmin(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func protectedWithChannelRole(minRole ChannelRole, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := channelSlugFromCtx(r)
		if !hasChannelRole(r, slug, minRole) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		handler(w, r)
	}
}

func getPrivilegeUsersList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	users, err := dbGetUsersList(ctx)
	if err != nil {
		http.Error(w, "Failed to get users list", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(users)
}

func setPrivilegeUsers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var req struct {
		List []User `json:"list"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	log.Println("Setting privileges for users:", req.List)

	if err := dbSetUsersList(ctx, req.List); err != nil {
		http.Error(w, "Failed to set users list", http.StatusInternalServerError)
		return
	}

	initializePrivilegeUsers()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{Success: true})
}
