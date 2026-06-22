package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	output "github.com/shhac/lib-agent-output"

	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// This file renders --format transcript for the grouped/list commands (unreads,
// later, drafts) on top of render.RenderGrouped — the digest sibling of the
// conversation transcript that backs message get/list. Each builder maps the
// command's compact structs into a render.Grouped; the JSON paths are untouched.

func transcriptOpts(globals *GlobalFlags, tflags *transcriptFlags) (render.TranscriptOptions, error) {
	loc, err := tflags.location()
	if err != nil {
		return render.TranscriptOptions{}, err
	}
	return render.TranscriptOptions{
		Loc:     loc,
		WithIDs: tflags.withIDs,
		Color:   output.Enabled(globals.stdout),
	}, nil
}

func writeGrouped(globals *GlobalFlags, g render.Grouped, opts render.TranscriptOptions) error {
	_, err := globals.stdout.Write([]byte(render.RenderGrouped(g, opts)))
	return err
}

// userNameResolver resolves a fixed set of user ids to display names up front
// (cache-then-fetch), returning "" for unknowns so SpeakerLine/speaker rendering
// falls back to the bare id. Backs both the grouped digests and the conversation
// transcript (transcriptUserResolver) off one display-name precedence.
func userNameResolver(ctx context.Context, cc *clientContext, ids []string) func(string) string {
	users, _ := slack.ResolveUsersByID(ctx, cc.Client, ids, slack.ResolveCacheThenFetch)
	return func(id string) string {
		u, ok := users[id]
		if !ok {
			return ""
		}
		return u.DisplayLabel()
	}
}

// authorIDName turns a compact author into the (id, display-name) pair
// SpeakerLine wants: a human user resolves via the resolver; a bot keeps its id
// (name falls back to the id); a nil author renders as unknown.
func authorIDName(a *render.CompactAuthor, resolve func(string) string) (id, name string) {
	if a == nil {
		return "", ""
	}
	if a.UserID != "" {
		return a.UserID, resolve(a.UserID)
	}
	return a.BotID, ""
}

// bodyLines splits pre-rendered content into transcript body lines; empty
// content yields no lines (the header stands alone).
func bodyLines(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

// countLabel renders "1 draft" / "3 drafts".
func countLabel(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

func replyNote(n int) string {
	if n <= 0 {
		return ""
	}
	return countLabel(n, "reply", "replies")
}

func groupedStamp(unix int64, loc *time.Location) string {
	return time.Unix(unix, 0).In(loc).Format(render.GroupedTimeLayout)
}

// --- canvas (document) -------------------------------------------------------

// writeCanvasTranscript prints a canvas as the document it is: its Markdown body
// verbatim under an optional dim `──── <title> ────` divider — the third
// transcript render family, neither conversation nor entity digest.
func writeCanvasTranscript(globals *GlobalFlags, c slack.Canvas) error {
	color := output.Enabled(globals.stdout)
	var b strings.Builder
	if c.Title != "" {
		b.WriteString(render.Dim("──── "+c.Title+" ────", color))
		b.WriteString("\n\n")
	}
	b.WriteString(c.Markdown)
	if !strings.HasSuffix(c.Markdown, "\n") {
		b.WriteString("\n")
	}
	_, err := globals.stdout.Write([]byte(b.String()))
	return err
}


// writeMessageGetFooter appends a dim `└ thread: N replies · <permalink>` line
// after a single-message transcript, surfacing the thread summary and permalink
// the JSON payload carries but the conversation render otherwise drops.
func writeMessageGetFooter(ctx context.Context, globals *GlobalFlags, cc *clientContext, ref *render.MessageRef, msg render.MessageSummary) error {
	color := output.Enabled(globals.stdout)
	permalink := render.BuildMessageURL(render.MessageURLParts{
		WorkspaceURL: ref.WorkspaceURL,
		ChannelID:    ref.ChannelID,
		MessageTS:    msg.TS,
		ThreadTS:     msg.ThreadTS,
	})
	footer := ""
	if thread, err := slack.ThreadSummary(ctx, cc.Client, ref.ChannelID, msg); err == nil && thread != nil {
		if replies := thread.Length - 1; replies > 0 {
			footer = fmt.Sprintf("thread: %s · ", countLabel(replies, "reply", "replies"))
		}
	}
	footer += permalink
	_, err := fmt.Fprintln(globals.stdout, render.Dim("└ "+footer, color))
	return err
}
