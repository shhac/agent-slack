package slack

import (
	"context"
	"regexp"
	"strings"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// This file turns SearchOptions into query primitives: the date validation and
// unix-range helpers the scan path filters on, plus the Slack search-syntax
// string the API path sends.

var searchDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func validateSearchDate(s string) (string, error) {
	v := strings.TrimSpace(s)
	if !searchDateRe.MatchString(v) {
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "invalid date: %s (expected YYYY-MM-DD)", s)
	}
	return v, nil
}

func dateToUnixSeconds(date string, endOfDay bool) (int64, error) {
	v, err := validateSearchDate(date)
	if err != nil {
		return 0, err
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return 0, agenterrors.Newf(agenterrors.FixableByAgent, "invalid date: %s (expected YYYY-MM-DD)", date)
	}
	if endOfDay {
		return t.Add(24*time.Hour - time.Millisecond).Unix(), nil
	}
	return t.Unix(), nil
}

// buildSearchQuery assembles Slack search syntax: query + after:/before: +
// from:@name + in:#name (IDs resolve to names — search syntax wants names).
func buildSearchQuery(ctx context.Context, c *Client, opts SearchOptions) (string, error) {
	var parts []string
	if base := strings.TrimSpace(opts.Query); base != "" {
		parts = append(parts, base)
	}
	if opts.After != "" {
		v, err := validateSearchDate(opts.After)
		if err != nil {
			return "", err
		}
		parts = append(parts, "after:"+v)
	}
	if opts.Before != "" {
		v, err := validateSearchDate(opts.Before)
		if err != nil {
			return "", err
		}
		parts = append(parts, "before:"+v)
	}
	if opts.User != "" {
		if token := userTokenForSearch(ctx, c, opts.User); token != "" {
			parts = append(parts, token)
		}
	}
	for _, ch := range opts.Channels {
		if token := channelTokenForSearch(ctx, c, ch); token != "" {
			parts = append(parts, token)
		}
	}
	return strings.Join(parts, " "), nil
}

func userTokenForSearch(ctx context.Context, c *Client, user string) string {
	trimmed := strings.TrimSpace(user)
	if trimmed == "" {
		return ""
	}
	if rest, ok := strings.CutPrefix(trimmed, "@"); ok {
		return "from:@" + rest
	}
	if render.IsUserID(trimmed) {
		resp, err := c.API(ctx, "users.info", map[string]any{"user": trimmed})
		if err != nil {
			return ""
		}
		if name := strings.TrimSpace(getStr(getRec(resp, "user"), "name")); name != "" {
			return "from:@" + name
		}
		return ""
	}
	return "from:@" + trimmed
}

func channelTokenForSearch(ctx context.Context, c *Client, channel string) string {
	id, name := NormalizeChannelInput(channel)
	if id == "" {
		if name = strings.TrimSpace(name); name == "" {
			return ""
		}
		return "in:#" + name
	}
	resp, err := c.API(ctx, "conversations.info", map[string]any{"channel": id})
	if err != nil {
		return ""
	}
	if chName := strings.TrimSpace(getStr(getRec(resp, "channel"), "name")); chName != "" {
		return "in:#" + chName
	}
	return ""
}
