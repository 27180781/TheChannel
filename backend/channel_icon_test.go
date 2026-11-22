package main

import (
	"strings"
	"testing"
)

const iconTestIndex = `<head>
  <title>x</title>
  <link rel="icon" type="image/x-icon" href="/assets/favicon.ico">
  <link rel="icon" type="image/svg+xml" href="/assets/icon.svg">
  <link rel="apple-touch-icon" href="/assets/apple-touch-icon.png">
  <link rel="manifest" href="/assets/manifest.webmanifest">
</head><body><app-root></app-root></body>`

func TestChannelIconPath(t *testing.T) {
	ch := &ChannelData{Slug: "my-ch", LogoUrl: "/api/channel/my-ch/files/abc123"}
	if got, want := channelIconPath(ch), "/favicon.ico?slug=my-ch&v=abc123"; got != want {
		t.Fatalf("icon = %q, want %q", got, want)
	}
	if got := channelIconPath(&ChannelData{Slug: "a"}); got != "" {
		t.Fatalf("no logo: icon = %q, want empty", got)
	}
	gated := &ChannelData{Slug: "a", LogoUrl: "/api/channel/a/files/abc"}
	gated.Features.RequireAuthFiles = true
	if got := channelIconPath(gated); got != "" {
		t.Fatalf("files behind sign-in: icon = %q, want empty", got)
	}
}

func TestReplaceIconLinks(t *testing.T) {
	out := string(replaceIconLinks([]byte(iconTestIndex), "/favicon.ico?slug=a&v=b"))
	for _, gone := range []string{"/assets/favicon.ico", "/assets/icon.svg", "/assets/apple-touch-icon.png", "image/svg+xml"} {
		if strings.Contains(out, gone) {
			t.Errorf("still has %q:\n%s", gone, out)
		}
	}
	if strings.Count(out, `<link rel="icon" href="/favicon.ico?slug=a&amp;v=b">`) != 1 {
		t.Errorf("want exactly one channel icon link:\n%s", out)
	}
	if strings.Count(out, `<link rel="apple-touch-icon" href="/favicon.ico?slug=a&amp;v=b">`) != 1 {
		t.Errorf("want exactly one channel touch icon:\n%s", out)
	}
	if !strings.Contains(out, `rel="manifest"`) {
		t.Errorf("manifest link lost:\n%s", out)
	}
}

func TestInjectHeadTagsKeepsDefaultIconWithoutChannel(t *testing.T) {
	out := string(injectHeadTags([]byte(iconTestIndex), "https://x", "s", "/", pageMeta{Title: "t", Image: "i", Card: "summary"}, nil))
	if !strings.Contains(out, "/assets/icon.svg") {
		t.Fatalf("default icons should stay:\n%s", out)
	}
}
