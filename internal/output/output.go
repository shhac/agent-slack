// Package output re-exports the shared output contract from lib-agent-output,
// keeping the internal/output import path while the wire mechanism (format
// parsing, JSON/YAML encoding, error rendering) lives in one place. What stays
// local is agent-slack policy: the explicit-writer Print signature, the
// structured WriteNotice, the Slack-shaped Pagination/NDJSONWriter, and the YAML
// number-normalization. (Migration shim.)
package output

import (
	"bytes"
	"encoding/json"
	"io"
	"math"

	out "github.com/shhac/lib-agent-output"
	"gopkg.in/yaml.v3"
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

// init registers agent-slack's YAML encoder with lib-agent-output, so YAML
// support (and its yaml.v3 dependency) stays in this CLI while the core library
// remains dependency-free. The encoder keeps the number-normalization that
// renders whole floats as integers in YAML output.
func init() {
	out.RegisterEncoder(out.FormatYAML, func(v any) ([]byte, error) {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(normalizeYAMLNumbers(v)); err != nil {
			return nil, err
		}
		_ = enc.Close()
		return buf.Bytes(), nil
	})
}

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

type NDJSONWriter struct {
	enc *json.Encoder
}

func NewNDJSONWriter(w io.Writer) *NDJSONWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &NDJSONWriter{enc: enc}
}

func (n *NDJSONWriter) WriteItem(item any) error {
	return n.enc.Encode(item)
}

func (n *NDJSONWriter) WriteMetaLine(key string, value any) error {
	return n.enc.Encode(map[string]any{key: value})
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

func normalizeYAMLNumbers(v any) any {
	return walkTree(v, nil, func(leaf any) any {
		f, ok := leaf.(float64)
		if !ok || math.IsInf(f, 0) || math.IsNaN(f) || math.Trunc(f) != f {
			return leaf
		}
		return int64(f)
	})
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
