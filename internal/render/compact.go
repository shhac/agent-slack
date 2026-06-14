package render

import (
	"net/url"
	"strings"
)

// FileSummary is the typed shape of one Slack file attachment after API
// parsing (the pure part of the TS message-api-parsing). Enrichment of
// snippets via files.info needs the API and belongs to the client layer.
type FileSummary struct {
	ID                 string       `json:"id"`
	Name               string       `json:"name,omitempty"`
	Title              string       `json:"title,omitempty"`
	Mimetype           string       `json:"mimetype,omitempty"`
	Filetype           string       `json:"filetype,omitempty"`
	Mode               string       `json:"mode,omitempty"`
	Permalink          string       `json:"permalink,omitempty"`
	URLPrivate         string       `json:"url_private,omitempty"`
	URLPrivateDownload string       `json:"url_private_download,omitempty"`
	Size               int64        `json:"size,omitempty"`
	Snippet            *FileSnippet `json:"snippet,omitempty"`
}

// FileSnippet carries inline snippet content fetched by the client layer.
type FileSnippet struct {
	Content  string `json:"content,omitempty"`
	Language string `json:"language,omitempty"`
}

// ToFileSummary shapes one raw file object from a Slack message. Returns nil
// when the value is not a file record with an id.
func ToFileSummary(value any) *FileSummary {
	f, ok := asRecord(value)
	if !ok {
		return nil
	}
	id := str(f["id"])
	if id == "" {
		return nil
	}
	size, _ := f["size"].(float64)
	return &FileSummary{
		ID:                 id,
		Name:               str(f["name"]),
		Title:              str(f["title"]),
		Mimetype:           str(f["mimetype"]),
		Filetype:           str(f["filetype"]),
		Mode:               str(f["mode"]),
		Permalink:          str(f["permalink"]),
		URLPrivate:         str(f["url_private"]),
		URLPrivateDownload: str(f["url_private_download"]),
		Size:               int64(size),
	}
}

// MessageSummary is one Slack message after API parsing; the client layer
// fills it from conversations.history / conversations.replies responses.
// Blocks, Attachments, and Reactions stay as decoded JSON because rendering
// walks their loosely-specified shapes.
type MessageSummary struct {
	ChannelID   string        `json:"channel_id"`
	TS          string        `json:"ts"`
	ThreadTS    string        `json:"thread_ts,omitempty"`
	ReplyCount  int           `json:"reply_count,omitempty"`
	User        string        `json:"user,omitempty"`
	BotID       string        `json:"bot_id,omitempty"`
	Text        string        `json:"text,omitempty"`
	Blocks      []any         `json:"blocks,omitempty"`
	Attachments []any         `json:"attachments,omitempty"`
	Files       []FileSummary `json:"files,omitempty"`
	Reactions   []any         `json:"reactions,omitempty"`
}

// DownloadResult records the outcome of one file download performed by the
// client layer; ToCompactMessage only reports it.
type DownloadResult struct {
	OK    bool
	Path  string
	Error string
}

// CompactMessage is the trimmed message shape emitted by read commands.
// ChannelID is omitempty because thread listings blank it (the channel is in
// the list's meta line instead).
type CompactMessage struct {
	ChannelID        string            `json:"channel_id,omitempty"`
	TS               string            `json:"ts"`
	ThreadTS         string            `json:"thread_ts,omitempty"`
	Author           *CompactAuthor    `json:"author,omitempty"`
	Content          string            `json:"content,omitempty"`
	Files            []CompactFile     `json:"files,omitempty"`
	Reactions        []CompactReaction `json:"reactions,omitempty"`
	ForwardedThreads []ForwardedThread `json:"forwarded_threads,omitempty"`
}

type CompactAuthor struct {
	UserID string `json:"user_id,omitempty"`
	BotID  string `json:"bot_id,omitempty"`
}

// AuthorRef builds the author reference, or nil when neither id is known.
func AuthorRef(userID, botID string) *CompactAuthor {
	if userID == "" && botID == "" {
		return nil
	}
	return &CompactAuthor{UserID: userID, BotID: botID}
}

type CompactFile struct {
	Name     string `json:"name,omitempty"`
	Mimetype string `json:"mimetype,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Path     string `json:"path,omitempty"`
	Error    string `json:"error,omitempty"`
}

type CompactReaction struct {
	Name  string   `json:"name"`
	Users []string `json:"users"`
	// Count is set only when it differs from len(Users), i.e. Slack knows of
	// reactors it didn't enumerate.
	Count int `json:"count,omitempty"`
}

// ForwardedThread points at the original thread of a forwarded message.
type ForwardedThread struct {
	URL        string `json:"url"`
	ThreadTS   string `json:"thread_ts"`
	ChannelID  string `json:"channel_id,omitempty"`
	ReplyCount int    `json:"reply_count,omitempty"`
}

// DefaultMaxBodyChars is the default content truncation for read commands.
const DefaultMaxBodyChars = 8000

// TruncateBody caps s at maxChars runes (not bytes — bodies carry emoji),
// marking the cut with "\n…". Negative maxChars means unlimited.
func TruncateBody(s string, maxChars int) string {
	if maxChars < 0 {
		return s
	}
	if r := []rune(s); len(r) > maxChars {
		return string(r[:maxChars]) + "\n…"
	}
	return s
}

// CompactOptions controls ToCompactMessage.
type CompactOptions struct {
	// MaxBodyChars truncates rendered content (with a "\n…" marker). Zero
	// means DefaultMaxBodyChars; negative means unlimited.
	MaxBodyChars     int
	IncludeReactions bool
	// DownloadedPaths maps file ID → download outcome; files without an
	// entry are omitted entirely.
	DownloadedPaths map[string]DownloadResult
	// SlackMarkdown keeps the native Slack mrkdwn in the rendered content
	// instead of converting to standard Markdown.
	SlackMarkdown bool
}

// ToCompactMessage shapes a parsed message into the compact output form.
func ToCompactMessage(msg MessageSummary, opts CompactOptions) CompactMessage {
	maxBodyChars := opts.MaxBodyChars
	if maxBodyChars == 0 {
		maxBodyChars = DefaultMaxBodyChars
	}

	content := TruncateBody(renderContent(msg.Text, msg.Blocks, msg.Attachments, opts.SlackMarkdown), maxBodyChars)

	var files []CompactFile
	for _, f := range msg.Files {
		entry, ok := opts.DownloadedPaths[f.ID]
		if !ok {
			continue
		}
		cf := CompactFile{Name: f.Name, Mimetype: f.Mimetype, Mode: f.Mode, Path: entry.Path}
		if !entry.OK {
			cf.Error = entry.Error
		}
		files = append(files, cf)
	}

	threadTS := msg.ThreadTS
	if threadTS == "" && msg.ReplyCount > 0 {
		threadTS = msg.TS
	}

	author := AuthorRef(msg.User, msg.BotID)

	var reactions []CompactReaction
	if opts.IncludeReactions {
		reactions = CompactReactions(msg.Reactions)
	}

	return CompactMessage{
		ChannelID:        msg.ChannelID,
		TS:               msg.TS,
		ThreadTS:         threadTS,
		Author:           author,
		Content:          content,
		Files:            files,
		Reactions:        reactions,
		ForwardedThreads: ExtractForwardedThreads(msg.Attachments),
	}
}

// CompactReactions shapes raw reaction objects, keeping only verifiable user
// IDs and recording the count only when Slack reports more reactors than it
// enumerated.
func CompactReactions(reactions []any) []CompactReaction {
	var out []CompactReaction
	for _, rAny := range reactions {
		r, ok := asRecord(rAny)
		if !ok {
			continue
		}
		name := strings.TrimSpace(str(r["name"]))
		if name == "" {
			continue
		}
		users := []string{}
		for _, uAny := range asSlice(r["users"]) {
			if u := str(uAny); IsUserID(u) {
				users = append(users, u)
			}
		}
		reaction := CompactReaction{Name: name, Users: users}
		if c, ok := r["count"].(float64); ok && int(c) != len(users) {
			reaction.Count = int(c)
		}
		out = append(out, reaction)
	}
	return out
}

// ExtractForwardedThreads finds forwarded-message attachments whose from_url
// carries thread coordinates (?thread_ts=…&cid=…), so callers can offer the
// original thread as a follow-up read.
func ExtractForwardedThreads(attachments []any) []ForwardedThread {
	var out []ForwardedThread
	seen := map[string]bool{}
	for _, aAny := range attachments {
		a, ok := asRecord(aAny)
		if !ok {
			continue
		}
		fromURL := strings.TrimSpace(str(a["from_url"]))
		if fromURL == "" {
			continue
		}
		parsed, err := url.Parse(fromURL)
		if err != nil || parsed.Scheme == "" {
			continue
		}
		threadTS := strings.TrimSpace(parsed.Query().Get("thread_ts"))
		if !IsMessageTS(threadTS) {
			continue
		}
		key := fromURL + "::" + threadTS
		if seen[key] {
			continue
		}
		seen[key] = true

		ft := ForwardedThread{
			URL:       fromURL,
			ThreadTS:  threadTS,
			ChannelID: strings.TrimSpace(parsed.Query().Get("cid")),
		}
		if c, ok := a["reply_count"].(float64); ok {
			ft.ReplyCount = int(c)
		}
		out = append(out, ft)
	}
	return out
}
