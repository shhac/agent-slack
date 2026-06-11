// Package render holds the pure conversion layer ported from the TypeScript
// agent-slack: Slack permalink/target parsing, mrkdwn ↔ Markdown, Block Kit /
// rich_text rendering, outbound rich_text construction, and compaction of raw
// message JSON. Everything here is side-effect free — no network, no I/O — so
// the Slack client and CLI commands can be tested against it directly.
package render

// Raw Slack payloads arrive as decoded JSON (map[string]any / []any / float64).
// These helpers mirror the loose lookups the TS code did on `unknown` values:
// missing keys and wrong types collapse to zero values instead of erroring.

func asRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// truthy mirrors JavaScript truthiness for decoded JSON values, because the
// TS source gated forwarded-message handling on `Boolean(a.is_share)` and
// Slack sometimes sends these flags as 0/1 instead of booleans.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	default:
		return true
	}
}
