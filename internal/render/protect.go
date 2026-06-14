package render

import (
	"regexp"
	"strconv"
)

var protectStashRe = regexp.MustCompile("\x00(\\d+)\x00")

// Protect replaces every match of the given regexps (applied in order) with a
// NUL sentinel so a following transform leaves those spans untouched, and
// returns the masked text plus a restore func that puts the spans back. It is
// the one implementation of the mask-transform-restore pattern used by outbound
// escaping, inbound emphasis conversion, and mention resolution.
func Protect(text string, res ...*regexp.Regexp) (masked string, restore func(string) string) {
	var stash []string
	masked = text
	for _, re := range res {
		masked = re.ReplaceAllStringFunc(masked, func(m string) string {
			stash = append(stash, m)
			return "\x00" + strconv.Itoa(len(stash)-1) + "\x00"
		})
	}
	restore = func(in string) string {
		return protectStashRe.ReplaceAllStringFunc(in, func(m string) string {
			if idx, err := strconv.Atoi(m[1 : len(m)-1]); err == nil && idx < len(stash) {
				return stash[idx]
			}
			return m
		})
	}
	return masked, restore
}
