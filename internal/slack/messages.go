package slack

import (
	"context"
	"sort"
	"strconv"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// FetchMessage locates one message by channel+ts. conversations.history does
// not guarantee thread replies, so the lookup cascades: recent history around
// the ts → the thread named by the permalink's thread_ts hint → the ts itself
// as a thread root via conversations.replies.
func FetchMessage(ctx context.Context, c *Client, ref *render.MessageRef, includeReactions bool) (render.MessageSummary, error) {
	params := map[string]any{
		"channel":   ref.ChannelID,
		"latest":    ref.MessageTS,
		"inclusive": true,
		"limit":     5,
	}
	if includeReactions {
		params["include_all_metadata"] = true
	}
	history, err := c.API(ctx, "conversations.history", params)
	if err != nil {
		return render.MessageSummary{}, err
	}
	msg := findByTS(getArr(history, "messages"), ref.MessageTS)

	if msg == nil && ref.ThreadTSHint != "" {
		msg, err = findMessageInThread(ctx, c, ref.ChannelID, ref.ThreadTSHint, ref.MessageTS, includeReactions)
		if err != nil {
			return render.MessageSummary{}, err
		}
	}

	if msg == nil {
		rootParams := map[string]any{"channel": ref.ChannelID, "ts": ref.MessageTS, "limit": 1}
		if includeReactions {
			rootParams["include_all_metadata"] = true
		}
		if rootResp, rerr := c.API(ctx, "conversations.replies", rootParams); rerr == nil {
			msgs := getArr(rootResp, "messages")
			if len(msgs) > 0 {
				if root, ok := msgs[0].(map[string]any); ok && getStr(root, "ts") == ref.MessageTS {
					msg = root
				}
			}
		}
	}

	if msg == nil {
		return render.MessageSummary{}, agenterrors.New("message not found (no access or wrong URL)", agenterrors.FixableByAgent).
			WithHint("check the permalink/--ts and that this account can see the channel")
	}

	summary := SummaryFromRaw(ref.ChannelID, msg)
	if summary.TS == "" {
		summary.TS = ref.MessageTS
	}
	summary.Files = enrichFiles(ctx, c, summary.Files)
	return summary, nil
}

func findByTS(messages []any, ts string) map[string]any {
	for _, m := range recItems(messages) {
		if getStr(m, "ts") == ts {
			return m
		}
	}
	return nil
}

func findMessageInThread(ctx context.Context, c *Client, channelID, threadTS, targetTS string, includeReactions bool) (map[string]any, error) {
	params := map[string]any{"channel": channelID, "ts": threadTS, "limit": 200}
	if includeReactions {
		params["include_all_metadata"] = true
	}
	var found map[string]any
	err := EachPage(ctx, c, "conversations.replies", params, func(resp map[string]any) (bool, error) {
		if m := findByTS(getArr(resp, "messages"), targetTS); m != nil {
			found = m
			return false, nil
		}
		return true, nil
	})
	return found, err
}

// SummaryFromRaw shapes one raw API message into a MessageSummary.
func SummaryFromRaw(channelID string, m map[string]any) render.MessageSummary {
	var files []render.FileSummary
	for _, f := range getArr(m, "files") {
		if fs := render.ToFileSummary(f); fs != nil {
			files = append(files, *fs)
		}
	}
	return render.MessageSummary{
		ChannelID:   channelID,
		TS:          getStr(m, "ts"),
		ThreadTS:    getStr(m, "thread_ts"),
		ReplyCount:  int(getNum(m, "reply_count")),
		User:        getStr(m, "user"),
		BotID:       getStr(m, "bot_id"),
		Text:        getStr(m, "text"),
		Blocks:      getArr(m, "blocks"),
		Attachments: getArr(m, "attachments"),
		Files:       files,
		Reactions:   getArr(m, "reactions"),
	}
}

// enrichFiles fills gaps for snippet-mode files (and any file missing its
// download URL) via files.info, attaching inline snippet content. Best
// effort: lookup failures keep the original summary.
func enrichFiles(ctx context.Context, c *Client, files []render.FileSummary) []render.FileSummary {
	for i, f := range files {
		if f.Mode != "snippet" && f.URLPrivateDownload != "" {
			continue
		}
		info, err := c.API(ctx, "files.info", map[string]any{"file": f.ID})
		if err != nil {
			continue
		}
		file := getRec(info, "file")
		if file == nil {
			continue
		}
		fillIfEmpty(&files[i].Name, getStr(file, "name"))
		fillIfEmpty(&files[i].Title, getStr(file, "title"))
		fillIfEmpty(&files[i].Mimetype, getStr(file, "mimetype"))
		fillIfEmpty(&files[i].Filetype, getStr(file, "filetype"))
		fillIfEmpty(&files[i].Mode, getStr(file, "mode"))
		fillIfEmpty(&files[i].Permalink, getStr(file, "permalink"))
		fillIfEmpty(&files[i].URLPrivate, getStr(file, "url_private"))
		fillIfEmpty(&files[i].URLPrivateDownload, getStr(file, "url_private_download"))
		files[i].Snippet = &render.FileSnippet{
			Content:  getStr(file, "content"),
			Language: getStr(file, "filetype"),
		}
	}
	return files
}

func fillIfEmpty(dst *string, value string) {
	if *dst == "" {
		*dst = value
	}
}

// HistoryOptions controls FetchChannelHistory.
type HistoryOptions struct {
	ChannelID        string
	Limit            int // default 25, clamped to [1, 200]
	Latest, Oldest   string
	IncludeReactions bool
	// Reaction-name filters force include_all_metadata and page through
	// history (newest-first via latest) until Limit matches accumulate.
	WithReactions    []string
	WithoutReactions []string
}

// FetchChannelHistory lists recent channel messages, chronologically.
func FetchChannelHistory(ctx context.Context, c *Client, opts HistoryOptions) ([]render.MessageSummary, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = 25
	}
	limit = clampInt(limit, 1, 200)
	hasReactionFilters := len(opts.WithReactions) > 0 || len(opts.WithoutReactions) > 0
	pageLimit := limit
	if hasReactionFilters {
		pageLimit = 200
	}

	params := map[string]any{"channel": opts.ChannelID, "limit": pageLimit}
	if opts.Oldest != "" {
		params["oldest"] = opts.Oldest
	}
	if opts.IncludeReactions || hasReactionFilters {
		params["include_all_metadata"] = true
	}
	var out []render.MessageSummary
	err := eachHistoryPage(ctx, c, params, opts.Latest, func(messages []map[string]any, resp map[string]any) (bool, error) {
		for _, m := range messages {
			if hasReactionFilters && !passesReactionNameFilters(m, opts.WithReactions, opts.WithoutReactions) {
				continue
			}
			summary := SummaryFromRaw(opts.ChannelID, m)
			summary.Files = enrichFiles(ctx, c, summary.Files)
			out = append(out, summary)
			if len(out) >= limit {
				break
			}
		}
		if len(out) >= limit || !hasReactionFilters {
			return false, nil
		}
		return getBool(resp, "has_more"), nil
	})
	if err != nil {
		return nil, err
	}

	sortChronological(out)
	return out, nil
}

func passesReactionNameFilters(m map[string]any, withReactions, withoutReactions []string) bool {
	names := map[string]bool{}
	for _, r := range recItems(getArr(m, "reactions")) {
		if name := getStr(r, "name"); name != "" {
			names[name] = true
		}
	}
	for _, want := range withReactions {
		if !names[want] {
			return false
		}
	}
	for _, reject := range withoutReactions {
		if names[reject] {
			return false
		}
	}
	return true
}

// FetchThread returns every message of a thread, chronologically.
func FetchThread(ctx context.Context, c *Client, channelID, threadTS string, includeReactions bool) ([]render.MessageSummary, error) {
	params := map[string]any{"channel": channelID, "ts": threadTS, "limit": 200}
	if includeReactions {
		params["include_all_metadata"] = true
	}
	var out []render.MessageSummary
	err := EachPage(ctx, c, "conversations.replies", params, func(resp map[string]any) (bool, error) {
		for _, m := range recItems(getArr(resp, "messages")) {
			summary := SummaryFromRaw(channelID, m)
			summary.Files = enrichFiles(ctx, c, summary.Files)
			out = append(out, summary)
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	sortChronological(out)
	return out, nil
}

func sortChronological(messages []render.MessageSummary) {
	sort.SliceStable(messages, func(i, j int) bool {
		a, _ := strconv.ParseFloat(messages[i].TS, 64)
		b, _ := strconv.ParseFloat(messages[j].TS, 64)
		return a < b
	})
}

// ThreadInfo summarizes the thread a message belongs to.
type ThreadInfo struct {
	TS     string `json:"ts"`
	Length int    `json:"length"`
}

// ThreadSummary reports the thread root + total length for a message, or nil
// when the message is not part of a thread. Thread parents already carry
// reply_count; replies need one conversations.replies call for the root.
func ThreadSummary(ctx context.Context, c *Client, channelID string, msg render.MessageSummary) (*ThreadInfo, error) {
	rootTS := msg.ThreadTS
	if rootTS == "" {
		if msg.ReplyCount <= 0 {
			return nil, nil
		}
		rootTS = msg.TS
	}

	if msg.ThreadTS == "" && msg.ReplyCount > 0 {
		return &ThreadInfo{TS: rootTS, Length: 1 + msg.ReplyCount}, nil
	}

	resp, err := c.API(ctx, "conversations.replies", map[string]any{
		"channel": channelID,
		"ts":      rootTS,
		"limit":   1,
	})
	if err != nil {
		return nil, err
	}
	msgs := recItems(getArr(resp, "messages"))
	if len(msgs) == 0 {
		return &ThreadInfo{TS: rootTS, Length: 1}, nil
	}
	count, ok := msgs[0]["reply_count"].(float64)
	if !ok {
		return &ThreadInfo{TS: rootTS, Length: 1}, nil
	}
	return &ThreadInfo{TS: rootTS, Length: 1 + int(count)}, nil
}
