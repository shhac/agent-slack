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
