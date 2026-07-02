package cli

import (
	"context"
	"strings"
	"time"

	libcli "github.com/shhac/lib-agent-cli/cli"
	"github.com/spf13/cobra"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// enableTranscript wires a command for --format transcript: it registers the
// display flags (--tz/--with-ids) and allow-lists the format. Bundling the two
// keeps them from drifting apart — a command that registers the flags but forgets
// AllowFormats silently rejects the format. (Canvas opts in without the display
// flags, so it calls libcli.AllowFormats directly.)
func enableTranscript(cmd *cobra.Command, tflags *transcriptFlags) {
	tflags.register(cmd)
	libcli.AllowFormats(cmd, transcriptFormat)
}

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
	// resolve is the --resolve mode for the grouped/digest transcripts
	// (unreads/later/drafts), which carry no readFlags. Registered only on those
	// commands (via registerResolveFlag); the conversation transcripts source it
	// from readFlags instead. Empty on commands that never register it → auto.
	resolve string
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
// It resolves referenced users, channels, and usergroups so speakers, mentions,
// and reactors read as names — under the --resolve policy (default auto, since a
// transcript is for humans), the same cache-controlled machinery the JSON
// referenced_* path uses. When threadMode is set, reply messages (thread_ts !=
// ts) indent one level under the root.
func printTranscript(ctx context.Context, globals *GlobalFlags, cc *clientContext, tflags *transcriptFlags, flags *readFlags, messages []render.MessageSummary, threadMode bool) error {
	opts, err := transcriptOpts(globals, tflags)
	if err != nil {
		return err
	}
	opts.SlackMarkdown = flags.slackMarkdown
	refs := render.CollectReferencedIDs(messages, true)
	resolvers := transcriptResolvers(ctx, globals, cc, refs, flags.resolveMode())
	opts.UserName, opts.ChannelName, opts.UsergroupName = resolvers.User, resolvers.Channel, resolvers.Usergroup
	opts.InlineEmoji = inlineEmojiResolver(ctx, globals, cc)

	items := make([]render.TranscriptMessage, 0, len(messages))
	for _, m := range messages {
		depth := 0
		if threadMode && m.ThreadTS != "" && m.ThreadTS != m.TS {
			depth = 1
		}
		items = append(items, render.TranscriptMessage{
			Summary: m,
			Depth:   depth,
		})
	}

	_, err = globals.stdout.Write([]byte(render.RenderTranscript(items, opts)))
	return err
}
