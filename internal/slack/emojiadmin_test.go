package slack

import (
	"context"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shhac/agent-slack/internal/mockslack"
)

// pngBytes is a minimal byte slice that http.DetectContentType reads as
// image/png (the 8-byte PNG signature is enough).
var pngBytes = []byte("\x89PNG\r\n\x1a\n" + "rest-of-file-bytes")

func writeTempImage(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAddEmojiUploadsImage(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.add", map[string]any{"ok": true})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	img := writeTempImage(t, "facepalm.png", pngBytes)
	stored, err := AddEmoji(context.Background(), c, ":My-Facepalm:", img)
	if err != nil {
		t.Fatal(err)
	}
	if stored != "my-facepalm" {
		t.Errorf("stored name = %q, want lowercased my-facepalm", stored)
	}
	calls := server.CallsFor("emoji.add")
	if len(calls) != 1 {
		t.Fatalf("emoji.add calls = %d, want 1", len(calls))
	}
	if calls[0].Params.Get("name") != "my-facepalm" || calls[0].Params.Get("mode") != "data" {
		t.Errorf("emoji.add params = %v, want name=my-facepalm mode=data", calls[0].Params)
	}
}

func TestAddEmojiAlias(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.add", map[string]any{"ok": true})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	if _, err := AddEmojiAlias(context.Background(), c, "shipit", ":squirrel:"); err != nil {
		t.Fatal(err)
	}
	p := server.CallsFor("emoji.add")[0].Params
	if p.Get("name") != "shipit" || p.Get("mode") != "alias" || p.Get("alias_for") != "squirrel" {
		t.Errorf("alias add params = %v", p)
	}
}

func TestRemoveEmoji(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.remove", map[string]any{"ok": true})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	if _, err := RemoveEmoji(context.Background(), c, "my-facepalm"); err != nil {
		t.Fatal(err)
	}
	p := server.CallsFor("emoji.remove")[0].Params
	if p.Get("name") != "my-facepalm" {
		t.Errorf("remove params = %v", p)
	}
}

// A successful mutation drops the cached set so the next list re-fetches.
func TestAddEmojiInvalidatesCache(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.list", map[string]any{"ok": true, "emoji": map[string]any{
		"old": "https://e/old.png",
	}})
	server.HandleBody("emoji.add", map[string]any{"ok": true})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	if _, _, err := ListEmoji(context.Background(), c, ListEmojiOptions{}); err != nil {
		t.Fatal(err)
	}
	img := writeTempImage(t, "new.png", pngBytes)
	if _, err := AddEmoji(context.Background(), c, "new", img); err != nil {
		t.Fatal(err)
	}
	// Cache was dropped → this list re-fetches.
	if _, _, err := ListEmoji(context.Background(), c, ListEmojiOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := len(server.CallsFor("emoji.list")); n != 2 {
		t.Errorf("emoji.list calls = %d, want 2 (cache invalidated after add)", n)
	}
}

func TestEmojiNameValidation(t *testing.T) {
	server := mockslack.New()
	server.HandleBody("emoji.add", map[string]any{"ok": true})
	c := cachingClient(t, server, "https://acme.slack.com", t.TempDir(), CacheNormal, time.Now())

	for _, bad := range []string{"", "  ", "has space", "with/slash", "emoji!"} {
		if _, err := RemoveEmoji(context.Background(), c, bad); err == nil {
			t.Errorf("name %q should be rejected", bad)
		}
	}
	// A bad image type is rejected before any API call.
	gif := writeTempImage(t, "notreally.png", []byte("GIF87a-not-a-png")) // sniffs as image/gif (allowed)
	if _, err := AddEmoji(context.Background(), c, "ok", gif); err != nil {
		t.Errorf("gif content should be accepted, got %v", err)
	}
	txt := writeTempImage(t, "note.png", []byte("just plain text, definitely not an image"))
	if _, err := AddEmoji(context.Background(), c, "ok", txt); err == nil {
		t.Error("non-image content should be rejected")
	}
}

// The multipart encoder produces a body the standard parser can read back,
// with the string fields and the file part intact.
func TestEncodeMultipartFile(t *testing.T) {
	enc := encodeMultipartFile(filePart{field: "image", filename: "a.png", contentType: "image/png", data: pngBytes})
	body, contentType, err := enc(map[string]string{"name": "x", "mode": "data"})
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
	form, err := r.ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	if form.Value["name"][0] != "x" || form.Value["mode"][0] != "data" {
		t.Errorf("fields = %v", form.Value)
	}
	fh := form.File["image"]
	if len(fh) != 1 || fh[0].Filename != "a.png" {
		t.Fatalf("file part = %v", fh)
	}
	f, _ := fh[0].Open()
	defer func() { _ = f.Close() }()
	got := make([]byte, len(pngBytes))
	_, _ = f.Read(got)
	if string(got) != string(pngBytes) {
		t.Errorf("file bytes round-tripped wrong")
	}
}
