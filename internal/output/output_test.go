package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	out "github.com/shhac/lib-agent-output"

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

// TestNDJSONWriterColorMode confirms list rows route through the shared color
// funnel: ANSI escapes when the color mode forces it on, byte-identical plain
// JSON when it's off. This is the property that was missing while the writer
// hand-rolled its own json.Encoder — color reached single-resource and
// json/yaml output but never the default NDJSON list path.
func TestNDJSONWriterColorMode(t *testing.T) {
	out.SetColorMode(out.ColorNever)
	defer out.SetColorMode(out.ColorAuto)

	var plain bytes.Buffer
	if err := NewNDJSONWriter(&plain).WriteItem(map[string]any{"ts": "1.2"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Errorf("ColorNever should emit no ANSI, got: %q", plain.String())
	}

	out.SetColorMode(out.ColorAlways)
	var colored bytes.Buffer
	if err := NewNDJSONWriter(&colored).WriteItem(map[string]any{"ts": "1.2"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Errorf("ColorAlways should emit ANSI on the NDJSON row, got: %q", colored.String())
	}
}

// YAML number normalization now lives in the shared lib-agent-cli/yaml encoder;
// this asserts Print still emits whole floats as plain integers (not scientific
// notation) and leaves fractions alone, end to end through that encoder.
func TestPrintYAMLNormalizesNumbers(t *testing.T) {
	var buf bytes.Buffer
	Print(&buf, map[string]any{
		"big":  float64(1770165109628379),
		"frac": 1.5,
	}, FormatYAML, false)
	got := buf.String()
	if !strings.Contains(got, "big: 1770165109628379") {
		t.Errorf("large whole float should render as integer, got:\n%s", got)
	}
	if !strings.Contains(got, "frac: 1.5") {
		t.Errorf("fractional float should render unchanged, got:\n%s", got)
	}
}

func TestWriteNotice(t *testing.T) {
	var buf bytes.Buffer
	WriteNotice(&buf, "cache was cold", "run 'cache warm'")
	payload := map[string]any{}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("notice should be JSON: %v (%q)", err, buf.String())
	}
	if payload["notice"] != "cache was cold" || payload["hint"] != "run 'cache warm'" {
		t.Errorf("notice payload = %v", payload)
	}

	// hint omitted when empty (fresh map — Unmarshal merges, doesn't clear).
	buf.Reset()
	WriteNotice(&buf, "just a notice", "")
	fresh := map[string]any{}
	_ = json.Unmarshal(buf.Bytes(), &fresh)
	if _, has := fresh["hint"]; has {
		t.Errorf("empty hint should be omitted: %s", buf.String())
	}
}

func TestWriteNoticeNilWriter(t *testing.T) {
	WriteNotice(nil, "x", "y") // must not panic on a nil writer
}
