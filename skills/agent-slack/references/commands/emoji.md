# emoji commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack emoji usage`.

Workspace **custom** emoji. These are for discovery — which custom names exist
and what aliases resolve to. To *use* an emoji in a message, just type
`:shortcode:`; Slack renders it (no command needed). The ~1.8k standard unicode
emoji are built in and not listed here, but `emoji get` falls back to them.

| Command | Notes |
|---|---|
| `emoji list` | `--full`, `--limit` (200, max 1000), `--cursor`; NDJSON sorted by name. Lean by default: `name` + `alias_for` (aliases). `--full` adds the image `url`. Custom set only; paginated via `{"@pagination":{next_cursor}}` |
| `emoji get <name…>` | `:colons:` optional; NDJSON default — one record or `{"@unresolved":{id,reason,fixable_by}}` per input in order; item-level miss → exit 0; `--format json` → object (one) or `{"data":[…],"@unresolved":[…]}` envelope (several). Unified lookup: custom → `{custom:true, url\|alias_for}`; alias followed one hop (→ `url` or `unicode`); standard name → `{unicode}`. Matched exactly (case-folded only; `-_+` not collapsed) |
| `emoji search <query>` | `--limit` (20, max 100), `--cursor`, `--full`; fuzzy-ranks **custom** emoji. Rows carry `match` (`exact\|prefix\|token_prefix\|contains\|fuzzy`) + `score`. Query is folded (case + `-_+` collapsed), so `parrot` finds `party-parrot`. Paginated via `{"@pagination":{next_cursor}}` |
| `emoji add <name> --image <path>` | **`--yes`**; uploads a png/gif/jpeg/webp as a new custom emoji. Needs a user/browser token |
| `emoji add <name> --alias-for <other>` | **`--yes`**; creates an alias to an existing emoji |
| `emoji remove <name>` | **`--yes`**; deletes a custom emoji |

`add`/`remove` are destructive (require `--yes`; without it they return what
would happen) and drop the `emoji` cache so the next `list`/`get` reflects the
change.

Backed by the per-workspace `emoji` cache (24h TTL). `cache warm emoji` pre-fills
it; within the window a name miss is authoritative (no refetch).
