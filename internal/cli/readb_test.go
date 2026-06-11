package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSearchMessages(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("search.messages", map[string]any{
		"ok": true,
		"messages": map[string]any{
			"matches": []any{map[string]any{
				"ts":        "1770165109.628379",
				"channel":   map[string]any{"id": "C12345678"},
				"permalink": "https://acme.slack.com/archives/C12345678/p1770165109628379",
			}},
			"paging": map[string]any{"pages": float64(1)},
		},
	})
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.628379", "U12345678", "found me"),
	))

	out, _, err := f.run(t, "search", "messages", "found")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 1 {
		t.Fatalf("lines = %v", lines)
	}
	if lines[0]["content"] != "found me" || lines[0]["permalink"] == "" {
		t.Errorf("hit = %v", lines[0])
	}
	if _, has := lines[0]["thread_ts"]; has {
		t.Error("search hits drop thread_ts")
	}
	if q := f.server.CallsFor("search.messages")[0].Params.Get("query"); q != "found" {
		t.Errorf("query = %q", q)
	}
}

func TestSearchQueryBuilding(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("search.messages", map[string]any{
		"ok":       true,
		"messages": map[string]any{"matches": []any{}},
	})

	_, _, err := f.run(t, "search", "messages", "deploy", "--after", "2026-06-01", "--before", "2026-06-10", "--user", "@paul")
	if err != nil {
		t.Fatal(err)
	}
	q := f.server.CallsFor("search.messages")[0].Params.Get("query")
	for _, want := range []string{"deploy", "after:2026-06-01", "before:2026-06-10", "from:@paul"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q missing %q", q, want)
		}
	}
}

func TestSearchInvalidDate(t *testing.T) {
	f := newCLIFixture(t)
	_, stderr, err := f.run(t, "search", "messages", "x", "--after", "June 1")
	if err == nil {
		t.Fatal("expected error")
	}
	if errPayload(t, stderr)["fixable_by"] != "agent" {
		t.Errorf("stderr = %s", stderr)
	}
}

func TestUnreads(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("client.counts", map[string]any{
		"ok": true,
		"channels": []any{map[string]any{
			"id": "C12345678", "has_unreads": true, "unread_count_display": float64(2),
			"mention_count": float64(1), "last_read": "1770165000.000000",
		}},
		"ims":     []any{},
		"threads": map[string]any{"has_unreads": true, "mention_count": float64(3)},
	})
	f.server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C12345678", "name": "general"},
	})
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.000001", "U1", "unread one"),
		map[string]any{"ts": "1770165110.000002", "user": "U2", "text": "joined", "subtype": "channel_join"},
	))

	out, _, err := f.run(t, "unreads")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 { // channel + @threads
		t.Fatalf("lines = %v", lines)
	}
	ch := lines[0]
	if ch["channel_name"] != "general" || ch["unread_count"] != float64(2) {
		t.Errorf("channel = %v", ch)
	}
	messages := ch["messages"].([]any)
	if len(messages) != 1 { // system join filtered
		t.Errorf("messages = %v", messages)
	}
	threads := lines[1]["@threads"].(map[string]any)
	if threads["mention_count"] != float64(3) {
		t.Errorf("threads = %v", threads)
	}
}

func TestLaterList(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("saved.list", map[string]any{
		"ok": true,
		"counts": map[string]any{
			"uncompleted_count": float64(2), "archived_count": float64(1),
			"completed_count": float64(5), "total_count": float64(8),
		},
		"saved_items": []any{
			map[string]any{"item_id": "C12345678", "item_type": "message", "ts": "1770165109.000001", "state": "in_progress", "date_created": float64(1770000000)},
			map[string]any{"item_id": "F999", "item_type": "file", "state": "in_progress"},
		},
	})
	f.server.HandleBody("conversations.info", map[string]any{
		"ok": true, "channel": map[string]any{"id": "C12345678", "name": "general"},
	})
	f.server.HandleBody("conversations.history", historyWith(
		simpleMessage("1770165109.000001", "U1", "remember this"),
	))

	out, _, err := f.run(t, "later", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if len(lines) != 2 { // 1 item (file filtered) + @counts
		t.Fatalf("lines = %v", lines)
	}
	item := lines[0]
	if item["channel_name"] != "general" || item["state"] != "in_progress" {
		t.Errorf("item = %v", item)
	}
	if item["message"].(map[string]any)["content"] != "remember this" {
		t.Errorf("message = %v", item["message"])
	}
	counts := lines[1]["@counts"].(map[string]any)
	if counts["total"] != float64(8) {
		t.Errorf("counts = %v", counts)
	}
}

func TestLaterCompleteUsesMultipartMark(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("saved.update", map[string]any{"ok": true})

	_, _, err := f.run(t, "later", "complete", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	call := f.server.CallsFor("saved.update")[0]
	if !strings.HasPrefix(call.Header.Get("Content-Type"), "multipart/form-data") {
		t.Errorf("content-type = %q (saved.update needs multipart)", call.Header.Get("Content-Type"))
	}
	if call.Params.Get("mark") != "completed" || call.Params.Get("item_id") != "C1A2B3C4D" {
		t.Errorf("params = %v", call.Params)
	}
}

func TestLaterRemind(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("saved.update", map[string]any{"ok": true})

	out, _, err := f.run(t, "later", "remind", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "--in", "3h")
	if err != nil {
		t.Fatal(err)
	}
	if parseJSON(t, out)["remind_at"] == nil {
		t.Errorf("out = %s", out)
	}
	if got := f.server.CallsFor("saved.update")[0].Params.Get("date_due"); got == "" {
		t.Error("date_due not sent")
	}
}

// fileHost serves canvas/file bytes for download tests.
func fileHost(t *testing.T, contentType, body string) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestCanvasGet(t *testing.T) {
	f := newCLIFixture(t)
	host := fileHost(t, "text/html", `<html><body><main><h1>Plan</h1><p>Step <strong>one</strong></p></main></body></html>`)
	f.server.HandleBody("files.info", map[string]any{
		"ok": true,
		"file": map[string]any{
			"id": "F08012345AB", "title": "Q3 Plan",
			"url_private_download": host.URL + "/canvas",
		},
	})

	out, _, err := f.run(t, "canvas", "get", "F08012345AB")
	if err != nil {
		t.Fatal(err)
	}
	canvas := parseJSON(t, out)["canvas"].(map[string]any)
	md := canvas["markdown"].(string)
	if !strings.Contains(md, "# Plan") || !strings.Contains(md, "**one**") {
		t.Errorf("markdown = %q", md)
	}
	if canvas["title"] != "Q3 Plan" {
		t.Errorf("canvas = %v", canvas)
	}
}

func TestFileDownload(t *testing.T) {
	f := newCLIFixture(t)
	host := fileHost(t, "image/png", "PNGBYTES")
	f.server.HandleBody("files.info", map[string]any{
		"ok": true,
		"file": map[string]any{
			"id": "F123ABC45", "name": "diagram.png", "mimetype": "image/png",
			"url_private_download": host.URL + "/f",
		},
	})

	out, _, err := f.run(t, "file", "download", "F123ABC45")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	path := payload["path"].(string)
	if !strings.HasSuffix(path, "F123ABC45.png") {
		t.Errorf("path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "PNGBYTES" {
		t.Errorf("file content = %q, err %v", data, err)
	}
}

func TestMessageGetDownloadsFiles(t *testing.T) {
	f := newCLIFixture(t)
	host := fileHost(t, "image/png", "IMGDATA")
	msg := simpleMessage("1770165109.628379", "U1", "see attachment")
	msg["files"] = []any{map[string]any{
		"id": "F77777777", "name": "shot.png", "mimetype": "image/png",
		"url_private_download": host.URL + "/shot",
	}}
	f.server.HandleBody("conversations.history", historyWith(msg))

	out, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379")
	if err != nil {
		t.Fatal(err)
	}
	files := parseJSON(t, out)["message"].(map[string]any)["files"].([]any)
	file := files[0].(map[string]any)
	if !strings.HasSuffix(file["path"].(string), "F77777777.png") {
		t.Errorf("file = %v", file)
	}

	// --no-download keeps it metadata-free (no downloadedPaths entry → no files key).
	out2, _, err := f.run(t, "message", "get", "https://acme.slack.com/archives/C1A2B3C4D/p1770165109628379", "--no-download")
	if err != nil {
		t.Fatal(err)
	}
	if _, has := parseJSON(t, out2)["message"].(map[string]any)["files"]; has {
		t.Error("--no-download should omit files (no local paths to report)")
	}
}

func TestAPICall(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("team.info", map[string]any{"ok": true, "team": map[string]any{"id": "T1", "name": "Acme"}})

	out, _, err := f.run(t, "api", "call", "team.info", "--params", `{"team":"T1"}`)
	if err != nil {
		t.Fatal(err)
	}
	payload := parseJSON(t, out)
	if payload["team"].(map[string]any)["name"] != "Acme" {
		t.Errorf("payload = %v", payload)
	}
	if got := f.server.CallsFor("team.info")[0].Params.Get("team"); got != "T1" {
		t.Errorf("param = %q", got)
	}
}

func TestWorkflowListAndRun(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("bookmarks.list", map[string]any{
		"ok": true,
		"bookmarks": []any{map[string]any{
			"id": "Bk1", "title": "Deploy", "shortcut_id": "Ft0001",
			"link": "https://slack.com/shortcuts/Ft0001/abc",
		}},
	})
	f.server.HandleBody("workflows.featured.list", map[string]any{"ok": false, "error": "unknown_method"})

	out, _, err := f.run(t, "workflow", "list", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	lines := parseNDJSON(t, out)
	if lines[0]["trigger_id"] != "Ft0001" {
		t.Errorf("workflow = %v", lines[0])
	}

	f.server.HandleBody("workflows.triggers.trip", map[string]any{
		"ok": true, "function_execution_id": "Fx1", "trigger_execution_id": "Tx1",
	})
	out, _, err = f.run(t, "workflow", "run", "Ft0001", "--channel", "C12345678")
	if err != nil {
		t.Fatal(err)
	}
	run := parseJSON(t, out)["run"].(map[string]any)
	if run["function_execution_id"] != "Fx1" {
		t.Errorf("run = %v", run)
	}
	trip := f.server.CallsFor("workflows.triggers.trip")[0]
	if trip.Params.Get("url") != "https://slack.com/shortcuts/Ft0001/abc" {
		t.Errorf("trip params = %v", trip.Params)
	}
}

func TestWorkflowGetSchema(t *testing.T) {
	f := newCLIFixture(t)
	f.server.HandleBody("workflows.get", map[string]any{
		"ok": true,
		"workflow": map[string]any{
			"id": "Wf001", "title": "Request",
			"steps": []any{map[string]any{
				"function": map[string]any{"callback_id": "open_form", "title": "Collect info"},
				"inputs": map[string]any{
					"title": map[string]any{"value": "Request form"},
					"fields": map[string]any{"value": map[string]any{
						"elements": []any{map[string]any{"name": "field-uuid-1", "title": "Summary", "type": "string"}},
						"required": []any{"field-uuid-1"},
					}},
				},
			}},
		},
	})

	out, _, err := f.run(t, "workflow", "get", "Wf001")
	if err != nil {
		t.Fatal(err)
	}
	schema := parseJSON(t, out)
	fields := schema["fields"].([]any)
	field := fields[0].(map[string]any)
	if field["title"] != "Summary" || field["required"] != true {
		t.Errorf("field = %v", field)
	}
	if schema["form_title"] != "Request form" {
		t.Errorf("schema = %v", schema)
	}
}
