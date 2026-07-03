package cli

import (
	"context"
	"maps"
	"slices"

	libcli "github.com/shhac/lib-agent-cli/cli"

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
	users, fetched := slack.ResolveUsersByID(ctx, c, ids, mode.policy())
	items := make([]any, 0, len(ids))
	for _, id := range ids {
		if u, ok := users[id]; ok {
			items = append(items, u)
		} else {
			items = append(items, map[string]any{"id": id}) // profile fetch failed; keep the id
		}
	}
	if fetched {
		maybeWarmHint(globals, mode, []string{"users"})
	}
	return printList(globals, items, meta)
}

// emitNotice writes a structured, non-fatal notice to stderr (hints/warnings),
// keeping stderr machine-parseable JSON like the error contract.
func emitNotice(globals *GlobalFlags, notice, hint string) {
	output.WriteNotice(globals.stderr, notice, hint)
}

func printSingle(globals *GlobalFlags, payload any) error {
	format, err := output.ResolveFormat(globals.Format, output.FormatJSON)
	if err != nil {
		return err
	}
	output.Print(globals.stdout, payload, format, true)
	return nil
}

// printOK emits the bare `{"ok": true}` acknowledgement shared by mutations
// that have nothing else to report.
func printOK(globals *GlobalFlags) error {
	return printSingle(globals, map[string]any{"ok": true})
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

// emitItem writes a single, already-resolved record per the family's get-output
// contract: NDJSON by default (one compact line), or the pretty bare object
// under --format json|yaml. Use for composite-key or singleton gets that don't
// fit the 1..N id model of runEntityGet.
func emitItem(globals *GlobalFlags, item any) error {
	return libcli.EmitItem(globals.stdout, globals.Format, item)
}

// runEntityGet is the shared body of the entity `get` commands (user, channel,
// usergroup, emoji). It delegates to the family's canonical EntityGet contract:
// NDJSON by default (one record or {"@unresolved":{…}} per id in input order),
// --format json|yaml → {data,@unresolved} envelope, exit 0 on item-level misses.
// Only command-level failures (auth, network) surface on stderr and exit 1.
func runEntityGet(globals *GlobalFlags, args []string, get func(arg string) (any, error)) error {
	return libcli.EntityGet(globals.stdout, globals.Format, args, func(id string) (any, error) {
		return get(id)
	})
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
