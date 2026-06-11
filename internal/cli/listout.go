package cli

import (
	"maps"
	"slices"

	"github.com/shhac/agent-slack/internal/output"
)

func resolveFormat(globals *GlobalFlags, def output.Format) (output.Format, error) {
	return output.ResolveFormat(globals.Format, def)
}

func printSingle(globals *GlobalFlags, payload any) error {
	format, err := resolveFormat(globals, output.FormatJSON)
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
	format, err := resolveFormat(globals, output.FormatNDJSON)
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
		payload[key] = value
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
