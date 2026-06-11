package credential

import "unicode/utf8"

// Redact masks a secret for display, keeping a short prefix and suffix so it
// stays recognizable without exposing the value. Short secrets become
// "[redacted]" entirely.
func Redact(value string) string {
	const keepStart, keepEnd = 6, 4
	if value == "" {
		return value
	}
	if utf8.RuneCountInString(value) <= keepStart+keepEnd+3 {
		return "[redacted]"
	}
	runes := []rune(value)
	return string(runes[:keepStart]) + "…" + string(runes[len(runes)-keepEnd:])
}
