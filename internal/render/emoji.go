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

// StripSkinTone removes the "::skin-tone-N" suffix Slack appends to a reaction
// name on the wire ("+1::skin-tone-3" -> "+1"). Total and lossless for names
// that carry no modifier, so it is safe to apply to any reaction name.
func StripSkinTone(name string) string {
	trimmed := strings.TrimSpace(name)
	if idx := strings.Index(trimmed, "::"); idx >= 0 {
		return trimmed[:idx]
	}
	return trimmed
}

// NormalizeReactionName converts ":rocket:", "rocket", "🚀", or the wire form
// "+1::skin-tone-3" to the bare name Slack's reactions API expects ("rocket",
// "+1"). One normalizer serves every command that takes an emoji, so
// `message react` and `message await --reaction` accept the same inputs.
func NormalizeReactionName(input string) (string, error) {
	trimmed := StripSkinTone(input)
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
