package render

import (
	"regexp"
	"strconv"
	"sync/atomic"
)

// Placeholders carry the id of the Protect call that made them, so a nested
// Protect's restore leaves the outer call's placeholders alone. Without that,
// an inner restore resolves outer indices against its own stash and swaps
// unrelated spans — masking code inside a pipeline that also masks angle spans
// is exactly that shape.
var protectStashRe = regexp.MustCompile("\x00(\\d+):(\\d+)\x00")

var protectSeq atomic.Uint64

// Protect replaces every match of the given regexps (applied in order) with a
// NUL sentinel so a following transform leaves those spans untouched, and
// returns the masked text plus a restore func that puts the spans back. It is
// the one implementation of the mask-transform-restore pattern used by outbound
// escaping, inbound emphasis conversion, and mention resolution.
func Protect(text string, res ...*regexp.Regexp) (masked string, restore func(string) string) {
	var stash []string
	id := strconv.FormatUint(protectSeq.Add(1), 10)
	masked = text
	for _, re := range res {
		masked = re.ReplaceAllStringFunc(masked, func(m string) string {
			stash = append(stash, m)
			return "\x00" + id + ":" + strconv.Itoa(len(stash)-1) + "\x00"
		})
	}
	restore = func(in string) string {
		return protectStashRe.ReplaceAllStringFunc(in, func(m string) string {
			sub := protectStashRe.FindStringSubmatch(m)
			if len(sub) != 3 || sub[1] != id {
				return m // another Protect call's placeholder; not ours to resolve
			}
			if idx, err := strconv.Atoi(sub[2]); err == nil && idx < len(stash) {
				return stash[idx]
			}
			return m
		})
	}
	return masked, restore
}
