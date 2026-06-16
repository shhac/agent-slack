package cli

import (
	"context"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// runForward handles `message send --forward`: it forwards the permalinked
// message into the target with an optional caption. Browser auth gets a native
// is_share card (chat.shareMessage); other tokens fall back to a permalink
// unfurl — both inside slack.ForwardMessage. Forwarding is its own send mode, so
// it rejects the flags that don't apply (blocks/attach/schedule/thread).
func runForward(ctx context.Context, globals *GlobalFlags, cc *clientContext, destChannelID, caption string, flags sendFlags) error {
	switch {
	case flags.blocksPath != "" || len(flags.attach) > 0:
		return agenterrors.New("--forward cannot be combined with --blocks or --attach", agenterrors.FixableByAgent)
	case flags.schedule != "" || flags.scheduleIn != "":
		return agenterrors.New("--forward cannot be scheduled", agenterrors.FixableByAgent)
	case flags.threadTS != "" || flags.replyBroadcast:
		return agenterrors.New("--forward does not support --thread-ts / --reply-broadcast", agenterrors.FixableByAgent)
	}

	ref, err := parseForwardTarget(flags.forward, cc.WorkspaceURL)
	if err != nil {
		return err
	}

	// The caption is an ordinary outbound message — mentions/#channels resolve
	// and it renders to the same rich_text blocks a normal send would carry.
	caption = slack.ResolveMentions(ctx, cc.Client, caption)
	caption = slack.ResolveChannelMentions(ctx, cc.Client, caption)
	outboundText, blocks := outboundTextAndBlocks(caption, flags.slackMarkdown)
	capMsg := slack.OutgoingMessage{Text: outboundText, Blocks: blocks}

	res, err := slack.ForwardMessage(ctx, cc.Client, destChannelID, slack.ForwardSource{
		ChannelID: ref.ChannelID,
		TS:        ref.MessageTS,
		Permalink: ref.Raw,
	}, capMsg)
	if err != nil {
		return err
	}
	return printSingle(globals, postedMessagePayload(res, cc.WorkspaceURL, ""))
}

// resolveForward folds a --forward permalink into draft text: an optional
// comment, then the permalink so Slack unfurls it into a shared-message card
// when the human sends the draft. Drafts use this embed form rather than the
// native chat.shareMessage forward (the send path's runForward) because
// shareMessage posts immediately and can't target a draft.
func resolveForward(comment, permalink, destWorkspaceURL string) (string, error) {
	ref, err := parseForwardTarget(permalink, destWorkspaceURL)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(comment) == "" {
		return ref.Raw, nil
	}
	return comment + "\n\n" + ref.Raw, nil
}

// parseForwardTarget parses a --forward permalink and rejects a cross-workspace
// one — a link to another workspace is a link, not a forward. Shared by the
// send (runForward) and draft (resolveForward) paths.
func parseForwardTarget(permalink, destWorkspaceURL string) (*render.MessageRef, error) {
	ref, err := render.ParseMessageURL(permalink)
	if err != nil {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "--forward: %v", err).
			WithHint("pass a Slack message permalink (https://…/archives/C…/p…)")
	}
	if !render.SameWorkspaceHost(ref.WorkspaceURL, destWorkspaceURL) {
		return nil, agenterrors.New("--forward cannot cross workspaces — a link to another workspace is a link, not a forward", agenterrors.FixableByAgent).
			WithHint("forward within the source workspace, or put the URL in the message text instead")
	}
	return ref, nil
}
