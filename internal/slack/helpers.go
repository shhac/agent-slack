package slack

// Loose lookups over decoded JSON, mirroring the TS object-type-guards:
// missing keys and wrong types collapse to zero values.

func getStr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func getNum(m map[string]any, key string) float64 {
	n, _ := m[key].(float64)
	return n
}

func getBool(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
}

func getRec(m map[string]any, key string) map[string]any {
	r, _ := m[key].(map[string]any)
	return r
}

func getArr(m map[string]any, key string) []any {
	a, _ := m[key].([]any)
	return a
}

// getStrArr extracts a []string from a JSON array of strings (non-strings and
// empties dropped), returning nil when absent — so a usergroup with no default
// channels omits the field rather than emitting [].
func getStrArr(m map[string]any, key string) []string {
	arr := getArr(m, key)
	if len(arr) == 0 {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

func recItems(values []any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for _, v := range values {
		if m, ok := v.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// orDefault substitutes def when v is the zero value.
func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
