package output

import (
	"testing"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// ParseFormat now comes from lib-agent-output, which is intentionally more
// lenient than agent-slack's original parser: it accepts the "ndjson" alias for
// "jsonl", a "yml" alias for YAML, and is case-insensitive. This pins that
// behavior so a lib bump that tightened it would fail here rather than silently
// regress the CLI's accepted --format values.
func TestParseFormatLenientAliases(t *testing.T) {
	cases := map[string]Format{
		"json":   FormatJSON,
		"JSON":   FormatJSON,
		"yaml":   FormatYAML,
		"yml":    FormatYAML,
		"YAML":   FormatYAML,
		"jsonl":  FormatNDJSON,
		"ndjson": FormatNDJSON,
		"NDJSON": FormatNDJSON,
	}
	for in, want := range cases {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

// An unknown format must still be rejected, and the rejection must carry the
// agent-fixable classification the CLI relies on to tell the agent it passed a
// bad flag value.
func TestParseFormatRejectsUnknownAsAgentFixable(t *testing.T) {
	_, err := ParseFormat("xml")
	var aerr *agenterrors.APIError
	if !agenterrors.As(err, &aerr) || aerr.FixableBy != agenterrors.FixableByAgent {
		t.Errorf("ParseFormat(xml) should return an agent-fixable APIError, got %v", err)
	}
}
