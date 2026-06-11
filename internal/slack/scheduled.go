package slack

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

const maxScheduleSeconds = 120 * 24 * 60 * 60

// ScheduledPage is one page of raw scheduled-message objects.
type ScheduledPage struct {
	ScheduledMessages []map[string]any
	NextCursor        string
}

// ScheduledListOptions controls ListScheduledMessages.
type ScheduledListOptions struct {
	ChannelID      string
	Cursor         string
	Oldest, Latest string
	Limit          int
}

func ListScheduledMessages(ctx context.Context, c *Client, opts ScheduledListOptions) (ScheduledPage, error) {
	params := map[string]any{}
	if opts.ChannelID != "" {
		params["channel"] = opts.ChannelID
	}
	if opts.Cursor != "" {
		params["cursor"] = opts.Cursor
	}
	if opts.Oldest != "" {
		params["oldest"] = opts.Oldest
	}
	if opts.Latest != "" {
		params["latest"] = opts.Latest
	}
	if opts.Limit > 0 {
		params["limit"] = opts.Limit
	}
	resp, err := c.API(ctx, "chat.scheduledMessages.list", params)
	if err != nil {
		return ScheduledPage{}, err
	}
	return ScheduledPage{
		ScheduledMessages: recItems(getArr(resp, "scheduled_messages")),
		NextCursor:        NextCursor(resp),
	}, nil
}

func CancelScheduledMessage(ctx context.Context, c *Client, channelID, scheduledMessageID string) error {
	_, err := c.API(ctx, "chat.deleteScheduledMessage", map[string]any{
		"channel":              channelID,
		"scheduled_message_id": scheduledMessageID,
	})
	return err
}

// ResolveSchedulePostAt turns --schedule / --schedule-in into a unix post_at.
// Returns 0 when neither is set.
func ResolveSchedulePostAt(schedule, scheduleIn string, now time.Time) (int64, error) {
	at := strings.TrimSpace(schedule)
	within := strings.TrimSpace(scheduleIn)
	if at != "" && within != "" {
		return 0, agenterrors.New("--schedule and --schedule-in are mutually exclusive", agenterrors.FixableByAgent)
	}
	if at == "" && within == "" {
		return 0, nil
	}

	var postAt int64
	var err error
	if at != "" {
		postAt, err = parseAbsoluteSchedule(at)
	} else {
		postAt, err = parseRelativeSchedule(within, now)
	}
	if err != nil {
		return 0, err
	}
	if postAt <= now.Unix() {
		return 0, agenterrors.New("--schedule/--schedule-in must resolve to a future time", agenterrors.FixableByAgent)
	}
	if postAt > now.Unix()+maxScheduleSeconds {
		return 0, agenterrors.New("--schedule/--schedule-in cannot be more than 120 days in the future", agenterrors.FixableByAgent)
	}
	return postAt, nil
}

var (
	relativeDurationRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(m|min|mins|minutes?|h|hr|hrs|hours?|d|day|days?)$`)
	isoPrefixRe        = regexp.MustCompile(`^(?i)\d{4}-\d{2}-\d{2}t\d{2}:\d{2}`)
	isoTimezoneRe      = regexp.MustCompile(`(?i)(?:z|[+-]\d{2}:?\d{2})$`)
	timeOfDayRe        = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?(am|pm)?$`)
)

func parseAbsoluteSchedule(input string) (int64, error) {
	if unix, ok := parseUnixTimestamp(input); ok {
		return unix, nil
	}
	scheduleErr := agenterrors.Newf(agenterrors.FixableByAgent,
		"invalid --schedule value %q", input).
		WithHint("use an ISO 8601 timestamp with an explicit timezone (YYYY-MM-DDTHH:mm:ss-07:00), or a unix timestamp")

	if !isoPrefixRe.MatchString(input) || !isoTimezoneRe.MatchString(input) {
		return 0, scheduleErr
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04Z07:00", "2006-01-02T15:04Z0700"} {
		if t, err := time.Parse(layout, input); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, scheduleErr
}

func parseRelativeSchedule(input string, now time.Time) (int64, error) {
	trimmed := strings.ToLower(strings.TrimSpace(input))
	scheduleErr := agenterrors.Newf(agenterrors.FixableByAgent,
		"invalid --schedule-in value %q", input).
		WithHint("use: 30m, 1h, 3h, 2d, tomorrow 9am, monday 9am, or a unix timestamp")
	if trimmed == "" {
		return 0, scheduleErr
	}
	if unix, ok := parseUnixTimestamp(trimmed); ok {
		return unix, nil
	}
	if m := relativeDurationRe.FindStringSubmatch(trimmed); m != nil {
		amount, _ := strconv.ParseFloat(m[1], 64)
		var seconds float64
		switch m[2][0] {
		case 'm':
			seconds = amount * 60
		case 'h':
			seconds = amount * 3600
		default:
			seconds = amount * 86400
		}
		return now.Unix() + int64(seconds), nil
	}
	if named, ok := parseNamedFutureTime(trimmed, now); ok {
		return named, nil
	}
	return 0, scheduleErr
}

func parseUnixTimestamp(input string) (int64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
	if err != nil || n < 1_000_000_000 {
		return 0, false
	}
	return int64(n), true
}

// parseNamedFutureTime handles "today 5pm", "tomorrow", "monday 9am",
// "next friday noon" — times default to 9am local.
func parseNamedFutureTime(input string, now time.Time) (int64, bool) {
	parts := strings.Fields(input)
	if len(parts) < 1 || len(parts) > 3 {
		return 0, false
	}

	hasNext := parts[0] == "next"
	rest := parts
	if hasNext {
		rest = parts[1:]
	}
	if len(rest) == 0 {
		return 0, false
	}
	dayToken := rest[0]
	timeText := strings.Join(rest[1:], " ")

	hour, minute := 9, 0
	if timeText != "" {
		var ok bool
		hour, minute, ok = parseTimeOfDay(timeText)
		if !ok {
			return 0, false
		}
	}

	at := func(daysFromNow int) time.Time {
		d := now.AddDate(0, 0, daysFromNow)
		return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, now.Location())
	}

	switch dayToken {
	case "today":
		t := at(0)
		if !t.After(now) {
			return 0, false
		}
		return t.Unix(), true
	case "tomorrow", "tmrw":
		return at(1).Unix(), true
	}

	targetDay, ok := weekdayIndex(dayToken)
	if !ok {
		return 0, false
	}
	daysUntil := targetDay - int(now.Weekday())
	if daysUntil < 0 || hasNext {
		daysUntil += 7
	}
	candidate := at(daysUntil)
	if !candidate.After(now) {
		candidate = at(daysUntil + 7)
	}
	return candidate.Unix(), true
}

func parseTimeOfDay(input string) (hour, minute int, ok bool) {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(input)), " ", "")
	switch normalized {
	case "noon":
		return 12, 0, true
	case "midnight":
		return 0, 0, true
	}
	m := timeOfDayRe.FindStringSubmatch(normalized)
	if m == nil {
		return 0, 0, false
	}
	hour, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		minute, _ = strconv.Atoi(m[2])
	}
	if minute < 0 || minute > 59 {
		return 0, 0, false
	}
	switch m[3] {
	case "am":
		if hour < 1 || hour > 12 {
			return 0, 0, false
		}
		if hour == 12 {
			hour = 0
		}
	case "pm":
		if hour < 1 || hour > 12 {
			return 0, 0, false
		}
		if hour != 12 {
			hour += 12
		}
	default:
		if hour > 23 {
			return 0, 0, false
		}
	}
	return hour, minute, true
}

func weekdayIndex(input string) (int, bool) {
	days := map[string]int{
		"sun": 0, "sunday": 0,
		"mon": 1, "monday": 1,
		"tue": 2, "tues": 2, "tuesday": 2,
		"wed": 3, "wednesday": 3,
		"thu": 4, "thur": 4, "thurs": 4, "thursday": 4,
		"fri": 5, "friday": 5,
		"sat": 6, "saturday": 6,
	}
	idx, ok := days[input]
	return idx, ok
}
