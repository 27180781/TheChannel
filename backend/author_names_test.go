package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A writer posts as "מנהל" until they choose otherwise, can switch to their
// registered name or any name they type, and an operator stays "ניהול".
func TestAuthorNameSignsPosts(t *testing.T) {
	useTestSessionStore(t)
	const slug = "author-name-post"
	operator, owner := operatorFixture(t, slug, "owner.alias@example.com")
	ctx := supportCtx(t)
	t.Cleanup(func() { rdb.Del(ctx, authorNamesKey(slug)) })

	post := func(sess Session) Message {
		t.Helper()
		r := requestAs(t, sess, slug, http.MethodPost, "/api/channel/"+slug+"/admin/new",
			strings.NewReader(`{"type":"md","text":"hi"}`), nil)
		w := httptest.NewRecorder()
		addMessage(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("addMessage: %d %q", w.Code, w.Body.String())
		}
		var m Message
		json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	set := func(sess Session, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := requestAs(t, sess, slug, http.MethodPost, "/api/channel/"+slug+"/admin/author-name",
			strings.NewReader(body), nil)
		w := httptest.NewRecorder()
		setAuthorName(w, r)
		return w
	}
	get := func(sess Session) authorNameResponse {
		t.Helper()
		r := requestAs(t, sess, slug, http.MethodGet, "/api/channel/"+slug+"/admin/author-name", nil, nil)
		w := httptest.NewRecorder()
		getAuthorName(w, r)
		var a authorNameResponse
		json.Unmarshal(w.Body.Bytes(), &a)
		return a
	}

	if a := get(owner); a.Mode != authorNameCustom || a.Name != defaultAuthorName || a.RegisteredName != "Channel Owner" {
		t.Errorf("default = %+v", a)
	}
	if m := post(owner); m.Author != defaultAuthorName {
		t.Errorf("default post signed %q", m.Author)
	}

	if w := set(owner, `{"mode":"custom","name":"  צוות   העריכה "}`); w.Code != http.StatusOK {
		t.Fatalf("set custom: %d %q", w.Code, w.Body.String())
	}
	if m := post(owner); m.Author != "צוות העריכה" {
		t.Errorf("custom post signed %q", m.Author)
	}

	if w := set(owner, `{"mode":"registered"}`); w.Code != http.StatusOK {
		t.Fatalf("set registered: %d", w.Code)
	}
	if m := post(owner); m.Author != "Channel Owner" {
		t.Errorf("registered post signed %q", m.Author)
	}

	for _, bad := range []string{`{"mode":"custom","name":"   "}`, `{"mode":"custom","name":"` + strings.Repeat("א", 41) + `"}`, `{"mode":"other"}`, `{"mode":"custom","name":"a‎b"}`} {
		if w := set(owner, bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s accepted: %d", bad, w.Code)
		}
	}

	if w := set(operator, `{"mode":"registered"}`); w.Code != http.StatusForbidden {
		t.Errorf("operator set: %d", w.Code)
	}
	if a := get(operator); !a.Operator || a.Name != operatorName || a.RegisteredName != "" {
		t.Errorf("operator get = %+v", a)
	}
	if m := post(operator); m.Author != operatorName {
		t.Errorf("operator post signed %q", m.Author)
	}
}
