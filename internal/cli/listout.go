package cli

import (
	"context"
	"maps"
	"slices"

	"github.com/shhac/agent-slack/internal/output"
	"github.com/shhac/agent-slack/internal/slack"
)

// printMembers is the shared body of the `members` commands (channel,
// usergroup): it prints member user ids as `{"id": …}` rows, or — when --users
// is cached/fresh — expands them to compact profiles, keeping the bare id when a
// profile fetch fails. meta carries any trailing meta lines (channel_id,
// pagination) the caller wants.
func printMembers(ctx context.Context, globals *GlobalFlags, c *slack.Client, ids []string, mode resolveMode, meta map[string]any) error {
	if !mode.resolve() {
		items := make([]any, len(ids))
		for i, id := range ids {
			items[i] = map[string]any{"id": id}
		}
		return printList(globals, items, meta)
	}
	users := slack.ResolveUsersByID(ctx, c, ids, mode.forceRefresh())
	items := make([]any, 0, len(ids))
	for _, id := range ids {
		if u, ok := users[id]; ok {
			items = append(items, u)
		} else {
			items = append(items, map[string]any{"id": id}) // profile fetch failed; keep the id
		}
	}
	return printList(globals, items, meta)
}

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

// runEntityGet is the shared body of the entity `get` commands (user, channel,
// usergroup): one arg prints the resolved object; several print NDJSON, then a
// trailing {"@unresolved": […]} for inputs that didn't resolve — a typo never
// drops the rest. get resolves one input to its output shape.
func runEntityGet(globals *GlobalFlags, args []string, get func(arg string) (any, error)) error {
	if len(args) == 1 {
		item, err := get(args[0])
		if err != nil {
			return err
		}
		return printSingle(globals, item)
	}
	var items []any
	var unresolved []string
	for _, arg := range args {
		item, err := get(arg)
		if err != nil {
			unresolved = append(unresolved, arg)
			continue
		}
		items = append(items, item)
	}
	return printList(globals, items, unresolvedMeta(unresolved))
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
