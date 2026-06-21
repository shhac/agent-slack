package cli

import (
	"context"
	"strings"
	"time"

	"github.com/spf13/cobra"

	output "github.com/shhac/lib-agent-output"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// transcriptFormat is the opt-in --format value rendering a conversation as
// natural-language text (not JSON). Only the conversation-read commands accept
// it (via libcli.AllowFormats); errors still go to stderr as structured JSON.
const transcriptFormat = "transcript"

// transcriptFlags are the display knobs for --format transcript. They register
// on every conversation-read command but only take effect when transcript is
// the resolved format.
type transcriptFlags struct {
	tz      string
	withIDs bool
}

func (f *transcriptFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.tz, "tz", "Local", "Transcript display zone: Local, UTC, or an IANA name (e.g. Europe/London)")
	cmd.Flags().BoolVar(&f.withIDs, "with-ids", false, "Append each message's ts id in the transcript header")
	// Color is driven by the global --color flag (lib-agent-cli); the transcript
	// renderer consults output.Enabled for the same per-stream decision.
}

// location resolves --tz to a time.Location: Local (honoring $TZ) and UTC are
// keywords; anything else must be a valid IANA zone or it's a structured,
// agent-fixable error.
func (f *transcriptFlags) location() (*time.Location, error) {
	switch strings.TrimSpace(f.tz) {
	case "", "Local", "local":
		return time.Local, nil
	case "UTC", "utc":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(strings.TrimSpace(f.tz))
	if err != nil {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent,
			"unknown timezone %q", f.tz).
			WithHint("use Local, UTC, or an IANA zone name like Europe/London")
	}
	return loc, nil
}


// wantsTranscript reports whether the resolved --format is the transcript
// renderer (the literal, since it lives outside the universal format enum).
func wantsTranscript(globals *GlobalFlags) bool {
	return string(globals.Format) == transcriptFormat
}

// printTranscript renders messages as a natural-language transcript on stdout.
// It always resolves referenced user names (so speakers/mentions/reactors read
// as names) regardless of --resolve, since a transcript is for humans. When
// threadMode is set, reply messages (thread_ts != ts) indent one level under
// the root.
func printTranscript(ctx context.Context, globals *GlobalFlags, cc *clientContext, tflags *transcriptFlags, slackMarkdown bool, messages []render.MessageSummary, threadMode bool) error {
	loc, err := tflags.location()
	if err != nil {
		return err
	}
	color := output.Enabled(globals.stdout)
	resolveUser := transcriptUserResolver(ctx, cc, messages)

	items := make([]render.TranscriptMessage, 0, len(messages))
	for _, m := range messages {
		depth := 0
		if threadMode && m.ThreadTS != "" && m.ThreadTS != m.TS {
			depth = 1
		}
		items = append(items, render.TranscriptMessage{
			Summary: m,
			Edited:  m.Edited,
			BotName: m.BotName,
			Depth:   depth,
		})
	}

	text := render.RenderTranscript(items, render.TranscriptOptions{
		Loc:           loc,
		WithIDs:       tflags.withIDs,
		SlackMarkdown: slackMarkdown,
		UserName:      resolveUser,
		Color:         color,
	})
	_, err = globals.stdout.Write([]byte(text))
	return err
}

// transcriptUserResolver builds an id→display-name lookup for the transcript,
// resolving every referenced user (cache-then-fetch) up front. Returns "" for
// ids it can't resolve so the renderer falls back to the bare id.
func transcriptUserResolver(ctx context.Context, cc *clientContext, messages []render.MessageSummary) func(string) string {
	userIDs := render.CollectReferencedUserIDs(messages, true)
	users, _ := slack.ResolveUsersByID(ctx, cc.Client, userIDs, slack.ResolveCacheThenFetch)
	return func(id string) string {
		u, ok := users[id]
		if !ok {
			return ""
		}
		if u.DisplayName != "" {
			return u.DisplayName
		}
		if u.RealName != "" {
			return u.RealName
		}
		return u.Name
	}
}
