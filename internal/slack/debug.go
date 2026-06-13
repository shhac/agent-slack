package slack

// The --debug trace: request params, response bodies, and soft-failure
// flagging — all redacted, because debug output lands in agent transcripts.
// The redaction contract is pinned by client_debug_test.go.

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

const debugBodyLimit = 2000

// debugRedactKeys are request param keys whose values are secrets.
var debugRedactKeys = map[string]bool{
	"token":       true,
	"xoxc_token":  true,
	"xoxd_cookie": true,
	"cookie":      true,
}

// tokenRe matches any Slack token (xoxc-, xoxb-, xoxp-, xoxd-, …) so it can be
// scrubbed from logged response bodies.
var tokenRe = regexp.MustCompile(`xox[a-zA-Z]-[A-Za-z0-9-]+`)

// debugf writes a single-line record to the debug writer.
func (c *Client) debugf(format string, args ...any) {
	if c.debug == nil {
		return
	}
	_, _ = fmt.Fprintf(c.debug, "slack: "+format+"\n", args...)
}

// debugParams logs the request params with secrets redacted and long values
// truncated. Only called when debug is on.
func (c *Client) debugParams(method string, fields map[string]string) {
	if c.debug == nil {
		return
	}
	parts := make([]string, 0, len(fields))
	for _, k := range slices.Sorted(maps.Keys(fields)) {
		v := fields[k]
		switch {
		case debugRedactKeys[strings.ToLower(k)] || strings.HasPrefix(v, "xox"):
			v = "[redacted]"
		case len(v) > 200:
			v = v[:200] + "…"
		}
		parts = append(parts, k+"="+v)
	}
	c.debugf("POST %s params {%s}", method, strings.Join(parts, " "))
}

// debugResponse logs the parsed response body, token-redacted and truncated.
func (c *Client) debugResponse(method string, data map[string]any) {
	if c.debug == nil {
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	s := tokenRe.ReplaceAllString(string(b), "[redacted]")
	if len(s) > debugBodyLimit {
		s = s[:debugBodyLimit] + "…(truncated)"
	}
	c.debugf("POST %s response %s", method, s)
}

// softFailureKeys are ok:true response fields that nonetheless signal the
// request did not fully succeed.
var softFailureKeys = []string{"rejected_triggers", "warning", "errors", "needed", "provided"}

// debugSoftFailure returns a short " soft-failure=…" suffix when an ok:true
// response carries fields that indicate a partial/soft failure.
func debugSoftFailure(data map[string]any) string {
	var present []string
	for _, k := range softFailureKeys {
		if v, ok := data[k]; ok && !isEmptyValue(v) {
			present = append(present, k)
		}
	}
	if len(present) == 0 {
		return ""
	}
	return " soft-failure=" + strings.Join(present, ",")
}

func isEmptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}
