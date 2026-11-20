package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

// The name a channel's writer signs their posts with, per channel.
//
// Readers never see who wrote a post: the feed shows them the channel's name
// (the /messages Lua masks the author). The signature is what the channel's
// own staff see on a post, and what goes out with it to the webhook and the
// event stream. A writer who does not want the rest of the staff to know it
// was them signs with a name of their choosing, and that is the default: until
// they change it, every writer posts as defaultAuthorName. They may switch to
// the name they registered with, or to any other name.
//
// An operator (super admin) is not covered: their posts are always signed
// operatorName, whatever is stored here.
//
// Storage: channel:<slug>:author_names, a hash from the writer's key
// (authorNameField) to a JSON AuthorName. No field means the default.
//
// The name is taken when a post is written (or a scheduled post is saved), so
// changing it does not re-sign posts that are already out.

const (
	defaultAuthorName   = "מנהל"
	maxAuthorNameRunes  = 40
	authorNameCustom    = "custom"
	authorNameRegistrar = "registered"
)

type AuthorName struct {
	// Mode is authorNameCustom (sign with Name) or authorNameRegistrar (sign
	// with the name the account registered with).
	Mode string `json:"mode"`
	Name string `json:"name,omitempty"`
}

func authorNamesKey(slug string) string { return fmt.Sprintf("channel:%s:author_names", slug) }

// authorNameField is the writer's hash field: their Google account id, or
// their email when the session predates ids.
func authorNameField(s Session) string {
	if s.ID != "" {
		return "id:" + s.ID
	}
	return "email:" + normEmail(s.Email)
}

// dbGetAuthorName returns the writer's stored choice, or the default.
func dbGetAuthorName(ctx context.Context, slug string, s Session) (AuthorName, error) {
	def := AuthorName{Mode: authorNameCustom, Name: defaultAuthorName}
	raw, err := rdb.HGet(ctx, authorNamesKey(slug), authorNameField(s)).Result()
	if err == redis.Nil {
		return def, nil
	}
	if err != nil {
		return def, err
	}
	var a AuthorName
	if json.Unmarshal([]byte(raw), &a) != nil {
		return def, nil
	}
	if a.Mode != authorNameRegistrar {
		a.Mode = authorNameCustom
		if strings.TrimSpace(a.Name) == "" {
			a.Name = defaultAuthorName
		}
	}
	return a, nil
}

// channelAuthorName is the name a post by s on slug is signed with. A read
// that fails signs with the default rather than the real name, since the
// default is what hides the writer.
func channelAuthorName(ctx context.Context, slug string, s Session) string {
	a, err := dbGetAuthorName(ctx, slug, s)
	if err != nil {
		log.Printf("channelAuthorName(%s): %v", slug, err)
	}
	if a.Mode == authorNameRegistrar {
		return sessionDisplayName(s)
	}
	return a.Name
}

// normaliseAuthorName validates a name a writer typed: trimmed, inner runs of
// whitespace collapsed, no control or invisible formatting characters, and
// 1..maxAuthorNameRunes long.
func normaliseAuthorName(name string) (string, error) {
	for _, r := range name {
		if unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r' {
			return "", fmt.Errorf("השם מכיל תווים לא חוקיים")
		}
		if unicode.Is(unicode.Cf, r) {
			return "", fmt.Errorf("השם מכיל תווים לא חוקיים")
		}
	}
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", fmt.Errorf("יש לכתוב שם")
	}
	if utf8.RuneCountInString(name) > maxAuthorNameRunes {
		return "", fmt.Errorf("השם ארוך מדי (עד %d תווים)", maxAuthorNameRunes)
	}
	return name, nil
}

type authorNameResponse struct {
	Mode           string `json:"mode"`
	Name           string `json:"name"`
	RegisteredName string `json:"registeredName"`
	DefaultName    string `json:"defaultName"`
	// Operator is set for a super admin, whose posts are always signed
	// operatorName and who has nothing to choose.
	Operator bool `json:"operator,omitempty"`
}

func writeAuthorName(w http.ResponseWriter, a AuthorName, s Session) {
	resp := authorNameResponse{
		Mode:           a.Mode,
		Name:           a.Name,
		RegisteredName: sessionDisplayName(s),
		DefaultName:    defaultAuthorName,
	}
	if resp.Mode == authorNameRegistrar {
		resp.Name = ""
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GET /api/channel/{slug}/admin/author-name (writer and up)
func getAuthorName(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if isSuperAdmin(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(authorNameResponse{Mode: authorNameCustom, Name: operatorName, DefaultName: defaultAuthorName, Operator: true})
		return
	}
	session, _ := store.Get(r, cookieName)
	user, ok := session.Values["user"].(Session)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	a, err := dbGetAuthorName(ctx, channelSlugFromCtx(r), user)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeAuthorName(w, a, user)
}

// POST /api/channel/{slug}/admin/author-name (writer and up)
//
// Body: {"mode":"custom","name":"..."} or {"mode":"registered"}. Each writer
// sets their own name only.
func setAuthorName(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if isSuperAdmin(r) {
		http.Error(w, "operator posts are always signed "+operatorName, http.StatusForbidden)
		return
	}
	session, _ := store.Get(r, cookieName)
	user, ok := session.Values["user"].(Session)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var req AuthorName
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	switch req.Mode {
	case authorNameRegistrar:
		req.Name = ""
	case authorNameCustom:
		name, err := normaliseAuthorName(req.Name)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
			return
		}
		req.Name = name
	default:
		http.Error(w, "invalid mode", http.StatusBadRequest)
		return
	}

	raw, _ := json.Marshal(req)
	if err := rdb.HSet(ctx, authorNamesKey(channelSlugFromCtx(r)), authorNameField(user), raw).Err(); err != nil {
		log.Printf("setAuthorName: %v", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	writeAuthorName(w, req, user)
}
