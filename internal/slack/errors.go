package slack

import (
	"errors"
	"fmt"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// codeError preserves the Slack error code (or HTTP status) under the mapped
// APIError so callers can branch on it (auth refresh, not-found handling)
// without string-matching messages.
type codeError struct {
	method string
	code   string
}

func (e *codeError) Error() string { return e.code + " calling " + e.method }

// ErrorCode returns the Slack error code (e.g. "channel_not_found") buried in
// an error returned by this package, or "".
func ErrorCode(err error) string {
	var ce *codeError
	if errors.As(err, &ce) {
		return ce.code
	}
	return ""
}

// authErrorCodes are credential failures: retrying with the same token is
// pointless, but a refreshed token can succeed. They drive both fixable_by:
// human mapping and the auto-refresh hook.
var authErrorCodes = map[string]bool{
	"invalid_auth":     true,
	"not_authed":       true,
	"token_expired":    true,
	"token_revoked":    true,
	"account_inactive": true,
}

// IsAuthError reports whether err is a Slack credential failure.
func IsAuthError(err error) bool {
	return authErrorCodes[ErrorCode(err)]
}

const reimportHint = "credentials are invalid or expired — re-import with 'agent-slack auth import-desktop' (or auth add / parse-curl)"

func mapSlackError(method, code string, data map[string]any) *agenterrors.APIError {
	if code == "" {
		code = "unknown_error"
	}
	cause := &codeError{method: method, code: code}
	apiErr := agenterrors.Wrap(cause, fixableForCode(code))

	switch {
	case authErrorCodes[code]:
		return apiErr.WithHint(reimportHint)
	case code == "missing_scope":
		hint := "the token lacks a required OAuth scope"
		if needed, _ := data["needed"].(string); needed != "" {
			hint += ": " + needed
		}
		return apiErr.WithHint(hint)
	case code == "ratelimited":
		return apiErr.WithHint("Slack rate limit hit — wait and retry")
	}

	// Slack often explains validation failures in response_metadata.messages
	// (e.g. invalid_blocks); surface that instead of the bare code.
	if hint := metadataMessages(data); hint != "" {
		return apiErr.WithHint(hint)
	}
	return apiErr
}

func fixableForCode(code string) agenterrors.FixableBy {
	switch {
	case authErrorCodes[code], code == "missing_scope":
		return agenterrors.FixableByHuman
	case code == "ratelimited":
		return agenterrors.FixableByRetry
	default:
		return agenterrors.FixableByAgent
	}
}

func metadataMessages(data map[string]any) string {
	raw := getArr(getRec(data, "response_metadata"), "messages")
	var parts []string
	for _, m := range raw {
		if s, ok := m.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	hint := strings.Join(parts, "; ")
	if len(hint) > 500 {
		hint = hint[:500] + "…"
	}
	return hint
}

func mapHTTPError(method string, status int) *agenterrors.APIError {
	cause := &codeError{method: method, code: fmt.Sprintf("HTTP %d", status)}
	apiErr := &agenterrors.APIError{
		Message: fmt.Sprintf("Slack HTTP %d calling %s", status, method),
		Cause:   cause,
	}
	switch {
	case status == 429:
		apiErr.FixableBy = agenterrors.FixableByRetry
		apiErr.Hint = "rate limited — wait and retry"
	case status >= 500:
		apiErr.FixableBy = agenterrors.FixableByRetry
		apiErr.Hint = "Slack server error — retry in a few seconds"
	case status == 401 || status == 403:
		apiErr.FixableBy = agenterrors.FixableByHuman
		apiErr.Hint = reimportHint
	default:
		apiErr.FixableBy = agenterrors.FixableByAgent
	}
	return apiErr
}

func mapNetworkError(method string, err error) *agenterrors.APIError {
	return agenterrors.Newf(agenterrors.FixableByRetry, "network error calling %s: %v", method, err).
		WithHint("check connectivity and retry; '--timeout' raises the request timeout")
}

func errBrowserNeedsWorkspace(method string) *agenterrors.APIError {
	return agenterrors.Newf(agenterrors.FixableByHuman,
		"browser auth requires a workspace URL (calling %s)", method).
		WithHint("pass --workspace, set SLACK_WORKSPACE_URL, or target a Slack message URL")
}
