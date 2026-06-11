package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

func TestWriteErrorContract(t *testing.T) {
	var buf bytes.Buffer
	err := agenterrors.New("invalid_auth", agenterrors.FixableByHuman).
		WithHint("Run 'agent-slack auth test' to verify credentials.")
	WriteError(&buf, err)

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("error output is not valid JSON: %v", err)
	}
	if payload["error"] != "invalid_auth" {
		t.Errorf("error = %v, want invalid_auth", payload["error"])
	}
	if payload["fixable_by"] != "human" {
		t.Errorf("fixable_by = %v, want human", payload["fixable_by"])
	}
	if payload["hint"] == "" {
		t.Error("expected hint to be present")
	}
}

func TestWriteErrorWrapsPlainErrors(t *testing.T) {
	var buf bytes.Buffer
	WriteError(&buf, errPlain{})

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("error output is not valid JSON: %v", err)
	}
	if payload["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v, want agent default", payload["fixable_by"])
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "plain failure" }

func TestParseFormat(t *testing.T) {
	for input, want := range map[string]Format{
		"json":   FormatJSON,
		"yaml":   FormatYAML,
		"jsonl":  FormatNDJSON,
		"ndjson": FormatNDJSON,
	} {
		got, err := ParseFormat(input)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %v, %v; want %v", input, got, err, want)
		}
	}

	_, err := ParseFormat("xml")
	var aerr *agenterrors.APIError
	if !agenterrors.As(err, &aerr) || aerr.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("ParseFormat(xml) should return an agent-fixable APIError, got %v", err)
	}
}

func TestNDJSONWriter(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	if err := w.WriteItem(map[string]any{"ts": "1.2"}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteMetaLine("@pagination", Pagination{HasMore: true, NextCursor: "abc"}); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d: %q", len(lines), buf.String())
	}
	for _, line := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Errorf("line %q is not valid JSON: %v", line, err)
		}
	}
}
