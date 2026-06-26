// Package output re-exports the shared output contract from lib-agent-output,
// keeping the internal/output import path while the wire mechanism (format
// parsing, JSON/YAML encoding, error rendering) lives in one place. What stays
// local is agent-slack policy: the explicit-writer Print signature, the
// structured WriteNotice, and the Slack-shaped Pagination/NDJSONWriter. The YAML
// encoder (with its whole-float-to-int normalization) comes from the shared
// lib-agent-cli/yaml package, blank-imported below. (Migration shim.)
package output

import (
	"encoding/json"
	"io"

	_ "github.com/shhac/lib-agent-cli/yaml"
	out "github.com/shhac/lib-agent-output"
)

// Format and its values come from the shared contract; ParseFormat is therefore
// the family's lenient parser (accepts "ndjson"/"yml", case-insensitive). The
// FormatNDJSON value is still the literal "jsonl" agent-slack has always used.
type Format = out.Format

const (
	FormatJSON   = out.FormatJSON
	FormatYAML   = out.FormatYAML
	FormatNDJSON = out.FormatNDJSON
)

var (
	ParseFormat   = out.ParseFormat
	ResolveFormat = out.ResolveFormat
	WriteError    = out.WriteError
)

// Print writes data to w in the given format, optionally pruning nulls. JSON
// prunes the typed value in place (a no-op for structs, matching the original);
// YAML round-trips so the number-normalization in the registered encoder sees a
// decoded tree.
func Print(w io.Writer, data any, format Format, prune bool) {
	if format == FormatYAML {
		decoded, ok := toDecoded(data)
		if !ok {
			return
		}
		if prune {
			decoded = pruneNulls(decoded)
		}
		_ = out.Print(w, decoded, FormatYAML, nil)
		return
	}
	if prune {
		data = pruneNulls(data)
	}
	_ = out.Print(w, data, FormatJSON, nil)
}

// WriteNotice emits a structured, non-fatal notice to w (typically stderr) —
// parallel to WriteError but informational, so stderr stays machine-parseable
// JSON rather than ad-hoc prose. hint is the optional actionable next step.
func WriteNotice(w io.Writer, notice, hint string) {
	if w == nil {
		return
	}
	payload := map[string]any{"notice": notice}
	if hint != "" {
		payload["hint"] = hint
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

// NDJSONWriter wraps the shared lib-agent-output writer so list output flows
// through the family's single encoding funnel — the same one Print and
// libcli.EmitItem use — rather than a hand-rolled json.Encoder. That funnel
// disables HTML escaping (URLs survive intact) and applies the per-stream color
// mode, so NDJSON rows colorize on a terminal exactly like JSON/YAML do, and
// stay byte-identical when piped. (Migration shim: keeps the internal/output
// import path while the wire mechanism lives in lib-agent-output.)
type NDJSONWriter struct {
	w *out.NDJSONWriter
}

func NewNDJSONWriter(w io.Writer) *NDJSONWriter {
	return &NDJSONWriter{w: out.NewNDJSONWriter(w)}
}

func (n *NDJSONWriter) WriteItem(item any) error {
	return n.w.WriteItem(item)
}

func (n *NDJSONWriter) WriteMetaLine(key string, value any) error {
	return n.w.WriteMetaLine(key, value)
}

// Pagination is Slack-shaped (an opaque next_cursor plus a total), so it stays
// local rather than using out.Pagination.
type Pagination struct {
	HasMore    bool   `json:"has_more"`
	TotalItems int    `json:"total_items,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func toDecoded(data any) (any, bool) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, false
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func pruneNulls(v any) any {
	return walkTree(v, func(child any) bool { return child == nil }, nil)
}

// walkTree rewrites a decoded JSON tree. dropKey, when non-nil, removes a map
// entry whose value it reports true for (before recursing); leaf, when non-nil,
// transforms each scalar (non-container) value. Only map entries are ever
// dropped — slice elements are always kept — which is what both callers need.
func walkTree(v any, dropKey func(any) bool, leaf func(any) any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			if dropKey != nil && dropKey(child) {
				continue
			}
			out[k] = walkTree(child, dropKey, leaf)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = walkTree(child, dropKey, leaf)
		}
		return out
	default:
		if leaf != nil {
			return leaf(val)
		}
		return val
	}
}
