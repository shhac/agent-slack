package output

import (
	"encoding/json"
	"io"
	"math"
	"os"
	"sync"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"gopkg.in/yaml.v3"
)

var (
	writersMu sync.Mutex
	stdout    io.Writer = os.Stdout
	stderr    io.Writer = os.Stderr
)

type Format string

const (
	FormatJSON   Format = "json"
	FormatYAML   Format = "yaml"
	FormatNDJSON Format = "jsonl"
)

func Stdout() io.Writer {
	writersMu.Lock()
	defer writersMu.Unlock()
	return stdout
}

func Stderr() io.Writer {
	writersMu.Lock()
	defer writersMu.Unlock()
	return stderr
}

func SetWriters(out, err io.Writer) func() {
	writersMu.Lock()
	oldOut, oldErr := stdout, stderr
	if out != nil {
		stdout = out
	}
	if err != nil {
		stderr = err
	}
	writersMu.Unlock()
	return func() {
		writersMu.Lock()
		stdout, stderr = oldOut, oldErr
		writersMu.Unlock()
	}
}

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

func Print(data any, format Format, prune bool) {
	switch format {
	case FormatYAML:
		printYAML(data, prune)
	default:
		printJSON(data, prune)
	}
}

func WriteRawJSON(raw json.RawMessage, format Format, prune bool) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return
	}
	Print(decoded, format, prune)
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

func printJSON(data any, prune bool) {
	if prune {
		data = pruneNulls(data)
	}
	enc := json.NewEncoder(Stdout())
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	_ = enc.Encode(data)
}

func printYAML(data any, prune bool) {
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
	enc := yaml.NewEncoder(Stdout())
	enc.SetIndent(2)
	_ = enc.Encode(decoded)
}

func normalizeYAMLNumbers(v any) any {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			val[k] = normalizeYAMLNumbers(child)
		}
		return val
	case []any:
		for i, child := range val {
			val[i] = normalizeYAMLNumbers(child)
		}
		return val
	case float64:
		if math.IsInf(val, 0) || math.IsNaN(val) || math.Trunc(val) != val {
			return val
		}
		return int64(val)
	default:
		return v
	}
}

func pruneNulls(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			if child == nil {
				continue
			}
			out[k] = pruneNulls(child)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = pruneNulls(child)
		}
		return out
	default:
		return v
	}
}
