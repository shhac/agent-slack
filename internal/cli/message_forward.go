package cli

import (
	"context"
	"net/url"
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

	ref, err := render.ParseMessageURL(flags.forward)
	if err != nil {
		return agenterrors.Newf(agenterrors.FixableByAgent, "--forward: %v", err).
			WithHint("pass a Slack message permalink (https://…/archives/C…/p…)")
	}
	if !sameSlackHost(ref.WorkspaceURL, cc.WorkspaceURL) {
		return agenterrors.New("--forward cannot cross workspaces — a link to another workspace is a link, not a forward", agenterrors.FixableByAgent).
			WithHint("forward within the source workspace, or put the URL in the message text instead")
	}

	// The caption is an ordinary outbound message — mentions/#channels resolve
	// and it renders to the same rich_text blocks a normal send would carry.
	caption = slack.ResolveMentions(ctx, cc.Client, caption)
	caption = slack.ResolveChannelMentions(ctx, cc.Client, caption)
	rtBlocks, outboundText := render.RenderOutbound(caption, flags.slackMarkdown)
	var blocks []any
	for _, b := range rtBlocks {
		blocks = append(blocks, b)
	}
	capMsg := slack.OutgoingMessage{Text: render.FormatOutboundText(outboundText), Blocks: blocks}

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

// resolveForward validates a --forward permalink against the destination
// workspace and folds it into the message text: an optional comment, then the
// permalink so Slack unfurls it into a shared-message card (the same is_share /
// is_msg_unfurl shape inbound rendering already understands). Slack exposes no
// API to embed a message's content directly — a true client "Forward" can't be
// driven over the public API — so a cross-workspace forward is impossible
// (unfurls are workspace- and permission-scoped); that case is a link, not a
// forward, and is rejected with a clear hint.
func resolveForward(comment, permalink, destWorkspaceURL string) (string, error) {
	ref, err := render.ParseMessageURL(permalink)
	if err != nil {
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "--forward: %v", err).
			WithHint("pass a Slack message permalink (https://…/archives/C…/p…)")
	}
	if !sameSlackHost(ref.WorkspaceURL, destWorkspaceURL) {
		return "", agenterrors.New("--forward cannot cross workspaces — a link to another workspace is a link, not a forward", agenterrors.FixableByAgent).
			WithHint("put the URL in the message text instead, or forward within the source workspace")
	}
	if strings.TrimSpace(comment) == "" {
		return ref.Raw, nil
	}
	return comment + "\n\n" + ref.Raw, nil
}

// sameSlackHost reports whether two workspace URLs share a hostname (the stable
// identity of a workspace — scheme/path/trailing slash may differ).
func sameSlackHost(a, b string) bool {
	ha, hb := slackHost(a), slackHost(b)
	return ha != "" && ha == hb
}

func slackHost(raw string) string {
	if u, err := url.Parse(strings.TrimSpace(raw)); err == nil && u.Hostname() != "" {
		return strings.ToLower(u.Hostname())
	}
	return ""
}
