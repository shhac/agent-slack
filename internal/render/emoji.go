package render

import (
	"regexp"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

var (
	emojiShortcodeRe    = regexp.MustCompile(`:[\w\-+]+:`)
	reactionShortcodeRe = regexp.MustCompile(`^:([^:\s]+):$`)
	reactionNameRe      = regexp.MustCompile(`^[A-Za-z0-9_+-]+$`)
)

// EmojifyShortcodes replaces known :emoji: shortcodes with their unicode
// character; unknown shortcodes are left untouched.
func EmojifyShortcodes(text string) string {
	if text == "" {
		return ""
	}
	return emojiShortcodeRe.ReplaceAllStringFunc(text, func(m string) string {
		if e, ok := emojiByName[m[1:len(m)-1]]; ok {
			return e
		}
		return m
	})
}

// EmojiUnicode returns the unicode character for a standard emoji shortcode
// name (no surrounding colons), from the static emojilib dataset. Custom
// workspace emoji are not in this set.
func EmojiUnicode(name string) (string, bool) {
	e, ok := emojiByName[name]
	return e, ok
}

// NormalizeReactionName converts ":rocket:", "rocket", or "🚀" to the bare
// name Slack's reactions API expects ("rocket").
func NormalizeReactionName(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", agenterrors.New("emoji is empty", agenterrors.FixableByAgent)
	}

	if m := reactionShortcodeRe.FindStringSubmatch(trimmed); m != nil {
		return m[1], nil
	}
	if reactionNameRe.MatchString(trimmed) {
		return trimmed, nil
	}
	if name, ok := emojiName(trimmed); ok {
		return name, nil
	}

	return "", agenterrors.Newf(agenterrors.FixableByAgent,
		"unsupported emoji format: %q (use :emoji: or unicode emoji)", input)
}

// emojiName reverse-looks-up a unicode emoji. Skin-tone modifiers and
// variation selector-16 are stripped first, matching node-emoji's `which`.
func emojiName(s string) (string, bool) {
	var b strings.Builder
	for _, r := range s {
		if r == 0xFE0F || (r >= 0x1F3FB && r <= 0x1F3FF) {
			continue
		}
		b.WriteRune(r)
	}
	name, ok := nameByEmoji[b.String()]
	return name, ok
}
