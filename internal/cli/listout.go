package cli

import (
	"sort"

	"github.com/shhac/agent-slack/internal/output"
)

func printSingle(globals *GlobalFlags, payload any) error {
	format, err := resolveFormat(globals, output.FormatJSON)
	if err != nil {
		return err
	}
	output.Print(payload, format, true)
	return nil
}

// printList writes NDJSON by default: one item per line, then any meta
// entries as `{"@key": value}` lines (referenced_users, pagination, …).
// json/yaml formats wrap everything in one envelope instead.
func printList(globals *GlobalFlags, items []any, meta map[string]any) error {
	format, err := resolveFormat(globals, output.FormatNDJSON)
	if err != nil {
		return err
	}
	if format == output.FormatNDJSON {
		w := output.NewNDJSONWriter(output.Stdout())
		for _, item := range items {
			if err := w.WriteItem(item); err != nil {
				return err
			}
		}
		for _, key := range sortedMetaKeys(meta) {
			if err := w.WriteMetaLine("@"+key, meta[key]); err != nil {
				return err
			}
		}
		return nil
	}

	payload := map[string]any{"data": items}
	for key, value := range meta {
		payload[key] = value
	}
	output.Print(payload, format, true)
	return nil
}

func sortedMetaKeys(meta map[string]any) []string {
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func toAnySlice[T any](items []T) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}

// listMeta builds the meta map, skipping empties.
func listMeta(pairs ...metaPair) map[string]any {
	meta := map[string]any{}
	for _, p := range pairs {
		if p.skip {
			continue
		}
		meta[p.key] = p.value
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}

type metaPair struct {
	key   string
	value any
	skip  bool
}

func metaPagination(nextCursor string) metaPair {
	return metaPair{
		key:   "pagination",
		value: output.Pagination{HasMore: nextCursor != "", NextCursor: nextCursor},
		skip:  nextCursor == "",
	}
}

func metaEntry(key string, value any, skip bool) metaPair {
	return metaPair{key: key, value: value, skip: skip}
}
