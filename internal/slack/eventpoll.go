package slack

// The standard-token fallback. client.getWebSocketURL is a client API, so a
// bot or user token cannot open the event socket at all; repeated history
// reads deliver the same events, just later and against a rate limit.

import (
	"context"
	"fmt"
	"strconv"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

const defaultPollEvery = 15 * time.Second

// runPoll is the standard-token fallback: no socket, just history reads after
// the cursor. Only usable with a single named conversation.
func (s *watchSession) runPoll(ctx context.Context) error {
	channel := s.opts.targetChannel()
	if channel == "" {
		return agenterrors.New(
			"polling requires a single conversation; the event socket is the only way to watch a whole workspace",
			agenterrors.FixableByHuman).
			WithHint("import browser credentials with 'agent-slack auth import-desktop'")
	}
	every := s.opts.PollEvery
	if every <= 0 {
		every = defaultPollEvery
	}
	cursor, err := s.pollBaseline(ctx)
	if err != nil {
		return err
	}
	for {
		if err := s.backfillChannel(ctx, channel, s.opts.Filter.ThreadTS, cursor); err != nil {
			return err
		}
		if s.stopped() {
			return nil
		}
		cursor = maxTS(cursor, s.watermark[channel])
		if err := s.client.sleep(ctx, every); err != nil {
			return nil
		}
	}
}

// pollBaseline is where a poll run starts reading from. With no --since there
// is no cursor, and "everything in history" is the wrong answer — the caller
// asked what happens next — so the conversation's current tip becomes the
// baseline.
func (s *watchSession) pollBaseline(ctx context.Context) (string, error) {
	if s.opts.Filter.Since != "" {
		return s.opts.Filter.Since, nil
	}
	messages, err := s.fetchTip(ctx)
	if err != nil {
		return "", err
	}
	if len(messages) == 0 {
		// An empty conversation has no tip to start from, and an empty cursor
		// makes every later read a no-op — the poll would spin until timeout
		// and report silence even as messages arrived. Start from now instead:
		// the caller asked what happens next, and nothing exists before it.
		return nowTS(), nil
	}
	return messages[len(messages)-1].TS, nil
}

func (s *watchSession) fetchTip(ctx context.Context) ([]render.MessageSummary, error) {
	channel := s.opts.targetChannel()
	if s.opts.Filter.ThreadTS != "" {
		return FetchThread(ctx, s.client, channel, s.opts.Filter.ThreadTS, false)
	}
	return FetchChannelHistory(ctx, s.client, HistoryOptions{ChannelID: channel, Limit: 1})
}

// nowTS renders the current time as a Slack timestamp, for the one case with
// no message to anchor to.
func nowTS() string {
	now := time.Now()
	return strconv.FormatInt(now.Unix(), 10) + "." + fmt.Sprintf("%06d", now.Nanosecond()/1000)
}
