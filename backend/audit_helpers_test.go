package main

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPushPreview(t *testing.T) {
	cases := []struct{ in, want string }{
		{"שלום **עולם**", "שלום עולם"},
		{"[image-embedded#640x480](/api/channel/x/files/abc)\nכותרת", "כותרת"},
		{"ראו [כאן](https://example.com/a?b=c) עכשיו", "ראו כאן עכשיו"},
		{"[quote-embedded#]ציטוט\n\n\nשורה", "ציטוט שורה"},
		{"  ", ""},
	}
	for _, c := range cases {
		if got := pushPreview(c.in); got != c.want {
			t.Errorf("pushPreview(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	long := strings.Repeat("א", 2*maxPushBodyRunes)
	got := pushPreview(long)
	if !utf8.ValidString(got) {
		t.Fatalf("preview is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(got); n != maxPushBodyRunes+1 {
		t.Errorf("preview length = %d runes, want %d + ellipsis", n, maxPushBodyRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated preview should end with an ellipsis")
	}
}

func TestTruncateRunesKeepsShortInput(t *testing.T) {
	if got := truncateRunes("abc", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
}

func TestContentDispositionAttachment(t *testing.T) {
	got := contentDispositionAttachment("דוח שנתי 2024.pdf")
	want := `attachment; filename="___ ____ 2024.pdf"; filename*=UTF-8''%D7%93%D7%95%D7%97%20%D7%A9%D7%A0%D7%AA%D7%99%202024.pdf`
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	// A space must never become "+": browsers do not decode it in filename*.
	if strings.Contains(rfc5987Encode("a b"), "+") {
		t.Errorf("space encoded as +")
	}
	if got := contentDispositionAttachment(`x"y\z.txt`); !strings.Contains(got, `filename="x_y_z.txt"`) {
		t.Errorf("quote/backslash not neutralised in fallback: %s", got)
	}
	if got := contentDispositionAttachment("שלום"); !strings.Contains(got, `filename="file"`) {
		t.Errorf("all-non-ascii name should fall back to a placeholder: %s", got)
	}
}

func TestTrimToRuneBoundary(t *testing.T) {
	// 5 Hebrew letters = 10 bytes; a cap of 7 bytes must not split a letter.
	got := trimTo("אבגדה", 7)
	if !utf8.ValidString(got) {
		t.Fatalf("trimTo produced invalid UTF-8: %q", got)
	}
	if got != "אבג" {
		t.Errorf("got %q, want %q", got, "אבג")
	}
	if got := trimTo("  abc  ", 10); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := trimTo("abcdef", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
}

func TestIsSafeContactURL(t *testing.T) {
	ok := []string{"", "https://example.com/contact", "http://wa.me/972500000000", "mailto:owner@example.com", "MAILTO:A@b.c"}
	bad := []string{"example.com", "javascript:alert(1)", "mailto:", "https://x y", "data:text/html,hi", "/relative"}
	for _, u := range ok {
		if !isSafeContactURL(u) {
			t.Errorf("%q should be accepted", u)
		}
	}
	for _, u := range bad {
		if isSafeContactURL(u) {
			t.Errorf("%q should be refused", u)
		}
	}
}

func TestSplitRegexRule(t *testing.T) {
	cases := []struct {
		rule, pat, rep string
		ok             bool
	}{
		{`(.*?\!)(.*)#**$1**$2`, `(.*?\!)(.*)`, `**$1**$2`, true},
		// A '#' inside the pattern is escaped by the form; the replacement
		// half keeps its own '#' characters as they are.
		{`\#(\S+)#**#$1**`, `\#(\S+)`, `**#$1**`, true},
		// An escaped backslash before the separator is not an escape of it.
		{`a\\#b`, `a\\`, `b`, true},
		{`no-separator`, ``, ``, false},
		{`#rep`, ``, `rep`, true},
	}
	for _, c := range cases {
		pat, rep, ok := splitRegexRule(c.rule)
		if pat != c.pat || rep != c.rep || ok != c.ok {
			t.Errorf("splitRegexRule(%q) = (%q, %q, %v), want (%q, %q, %v)", c.rule, pat, rep, ok, c.pat, c.rep, c.ok)
		}
	}
	// The escaped form must compile to a literal '#'.
	re, err := regexp.Compile(`\#(\S+)`)
	if err != nil {
		t.Fatalf("escaped '#' does not compile: %v", err)
	}
	if got := re.ReplaceAllString("see #tag now", "**#$1**"); got != "see **#tag** now" {
		t.Errorf("hashtag rule: got %q", got)
	}
}

func TestNormalizeUsersMergesCaseDuplicates(t *testing.T) {
	users := normalizeUsers([]User{
		{Email: "Owner@Example.com", ChannelRoles: map[string]ChannelRole{"a": RoleWriter}},
		{Email: " owner@example.com ", PublicName: "Owner", ChannelRoles: map[string]ChannelRole{"a": RoleOwner, "b": RoleModerator}},
		{Email: "", GlobalRole: RoleSuperAdmin},
		{Email: "Admin@Example.com", GlobalRole: RoleSuperAdmin},
	})
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2: %+v", len(users), users)
	}
	o := users[0]
	if o.Email != "owner@example.com" || o.PublicName != "Owner" {
		t.Errorf("merged owner: %+v", o)
	}
	if o.ChannelRoles["a"] != RoleOwner || o.ChannelRoles["b"] != RoleModerator {
		t.Errorf("merged roles keep the strongest: %+v", o.ChannelRoles)
	}
	if users[1].Email != "admin@example.com" || users[1].GlobalRole != RoleSuperAdmin {
		t.Errorf("admin: %+v", users[1])
	}
}

func TestIsUploadRoute(t *testing.T) {
	if !isUploadRoute("/api/channel/news/admin/upload") {
		t.Error("upload route not recognised")
	}
	for _, p := range []string{"/api/channel/news/admin/new", "/api/support/tickets", "/admin/upload", "/api/channel/x/admin/upload/extra"} {
		if isUploadRoute(p) {
			t.Errorf("%s wrongly exempt", p)
		}
	}
}
