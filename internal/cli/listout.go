package cli

import (
	"maps"
	"slices"

	"github.com/shhac/agent-slack/internal/output"
)

func printSingle(globals *GlobalFlags, payload any) error {
	format, err := output.ResolveFormat(globals.Format, output.FormatJSON)
	if err != nil {
		return err
	}
	output.Print(globals.stdout, payload, format, true)
	return nil
}

// printList writes NDJSON by default: one item per line, then any meta
// entries as `{"@key": value}` lines (referenced_users, pagination, …).
// json/yaml formats wrap everything in one envelope instead.
func printList(globals *GlobalFlags, items []any, meta map[string]any) error {
	format, err := output.ResolveFormat(globals.Format, output.FormatNDJSON)
	if err != nil {
		return err
	}
	if format == output.FormatNDJSON {
		w := output.NewNDJSONWriter(globals.stdout)
		for _, item := range items {
			if err := w.WriteItem(item); err != nil {
				return err
			}
		}
		for _, key := range slices.Sorted(maps.Keys(meta)) {
			if err := w.WriteMetaLine("@"+key, meta[key]); err != nil {
				return err
			}
		}
		return nil
	}

	payload := map[string]any{"data": items}
	for key, value := range meta {
		payload["@"+key] = value // @-prefixed, matching the NDJSON meta lines and the docs
	}
	output.Print(globals.stdout, payload, format, true)
	return nil
}

func toAnySlice[T any](items []T) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = item
	}
	return out
}

// unresolvedMeta is the trailing meta for a multi-arg get: an `@unresolved`
// list of the inputs that didn't resolve, or nil when everything resolved.
func unresolvedMeta(unresolved []string) map[string]any {
	if len(unresolved) == 0 {
		return nil
	}
	return map[string]any{"unresolved": unresolved}
}

// listMeta merges extra meta entries with pagination (added only when a
// next cursor exists). Returns nil when there is nothing to emit.
func listMeta(nextCursor string, extra map[string]any) map[string]any {
	meta := map[string]any{}
	maps.Copy(meta, extra)
	if nextCursor != "" {
		meta["pagination"] = output.Pagination{HasMore: true, NextCursor: nextCursor}
	}
	if len(meta) == 0 {
		return nil
	}
	return meta
}
