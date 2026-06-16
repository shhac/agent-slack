package output

import (
	"encoding/json"
	"io"
	"math"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"gopkg.in/yaml.v3"
)

type Format string

const (
	FormatJSON   Format = "json"
	FormatYAML   Format = "yaml"
	FormatNDJSON Format = "jsonl"
)

func ParseFormat(s string) (Format, error) {
	switch s {
	case "json":
		return FormatJSON, nil
	case "yaml":
		return FormatYAML, nil
	case "jsonl", "ndjson":
		return FormatNDJSON, nil
	default:
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "unknown format %q, expected: json, yaml, jsonl", s)
	}
}

func ResolveFormat(flagFormat string, defaultFormat Format) (Format, error) {
	if flagFormat == "" {
		return defaultFormat, nil
	}
	return ParseFormat(flagFormat)
}

// Print writes data to w in the given format, optionally pruning nulls.
func Print(w io.Writer, data any, format Format, prune bool) {
	switch format {
	case FormatYAML:
		printYAML(w, data, prune)
	default:
		printJSON(w, data, prune)
	}
}

func WriteError(w io.Writer, err error) {
	var aerr *agenterrors.APIError
	if !agenterrors.As(err, &aerr) {
		aerr = agenterrors.Wrap(err, agenterrors.FixableByAgent)
	}
	payload := map[string]any{
		"error":      aerr.Message,
		"fixable_by": string(aerr.FixableBy),
	}
	if aerr.Hint != "" {
		payload["hint"] = aerr.Hint
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
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

type Pagination struct {
	HasMore    bool   `json:"has_more"`
	TotalItems int    `json:"total_items,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func printJSON(w io.Writer, data any, prune bool) {
	if prune {
		data = pruneNulls(data)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(data)
}

func printYAML(w io.Writer, data any, prune bool) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	var decoded any
	if err := json.Unmarshal(b, &decoded); err != nil {
		return
	}
	if prune {
		decoded = pruneNulls(decoded)
	}
	decoded = normalizeYAMLNumbers(decoded)
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	_ = enc.Encode(decoded)
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
