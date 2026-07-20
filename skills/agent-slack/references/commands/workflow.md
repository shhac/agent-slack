# workflow commands

Global flags and the `--yes` gate convention: see the [commands index](../commands.md).
In-binary version: `agent-slack workflow usage`.

| Command | Notes |
|---|---|
| `workflow list <channel>` | triggers (`Ft…`) published in a channel; each row carries `stale: true` + `stale_reason` when its trigger can't be previewed (a lingering bookmark) |
| `workflow preview <Ft…>` | trigger metadata + its workflow id (`Wf…`) |
| `workflow get <Ft…\|Wf…>` | form fields + step titles |
| `workflow run <Ft…> --channel <ch> --field "Title=value"` | submit a form; needs **browser auth** (xoxc/xoxd) + an RTM WebSocket |

Workflow discovery is channel-by-channel. `workflow list` validates every
listed trigger in one batched call, so stale bookmarks (deleted workflows →
`stale_reason: trigger_not_found`) and inaccessible ones are flagged inline
rather than only failing when you `preview` them — trust a row without `stale`.
The whole annotated list is cached per channel, and validating it also warms
each live trigger's preview cache.

## Form field values

`--field "Title=value"` values map to the form's input types automatically:

| Form input | Value format |
|---|---|
| Short answer / paragraph / rich text / number | verbatim string |
| Drop-down / multiple choice | one option, matched by label (case-insensitive) or value |
| Tick boxes | comma-separated options (labels with commas: match by value) |
| Date | `YYYY-MM-DD` |
| File upload | unsupported — errors; use a Slack client |

Slack reports form validation failures inside an `ok:true` response
(`response_action: "errors"`); the CLI surfaces those as real errors, so
`submitted: true` means the form actually cleared. If a run errors after
tripping (bad option, unsupported field), the CLI closes the opened form,
cancelling that run — fix the value and rerun.
