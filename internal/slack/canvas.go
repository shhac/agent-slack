package slack

import (
	"context"
	"net/url"
	"os"
	"regexp"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
)

// CanvasRef identifies a canvas extracted from a /docs/ URL.
type CanvasRef struct {
	WorkspaceURL string
	CanvasID     string // a file id, e.g. F080JDE025R
	Raw          string
}

var canvasIDRe = regexp.MustCompile(`^F[A-Z0-9]{8,}$`)

// IsCanvasID reports whether s looks like a canvas (file) ID.
func IsCanvasID(s string) bool {
	return canvasIDRe.MatchString(s)
}

// ParseCanvasURL parses https://{workspace}/docs/{team}/{canvas_id} links.
func ParseCanvasURL(input string) (*CanvasRef, error) {
	u, err := url.Parse(input)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "invalid URL: %s", input)
	}
	if !strings.HasSuffix(strings.ToLower(u.Hostname()), ".slack.com") {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "not a Slack workspace URL: %s", u.Hostname())
	}
	parts := []string{}
	for _, p := range strings.Split(u.Path, "/") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 || parts[0] != "docs" {
		return nil, agenterrors.Newf(agenterrors.FixableByAgent, "unsupported Slack canvas URL path: %s", u.Path)
	}
	for _, p := range parts {
		if IsCanvasID(p) {
			return &CanvasRef{
				WorkspaceURL: u.Scheme + "://" + strings.ToLower(u.Host),
				CanvasID:     p,
				Raw:          input,
			}, nil
		}
	}
	return nil, agenterrors.Newf(agenterrors.FixableByAgent, "could not find canvas id in: %s", u.Path)
}

// Canvas is the output of FetchCanvasMarkdown.
type Canvas struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Markdown string `json:"markdown"`
}

// CanvasOptions controls FetchCanvasMarkdown.
type CanvasOptions struct {
	MaxChars     int // 0 → 20000, negative → unlimited
	DownloadsDir string
	// HTMLToMarkdown converts the canvas HTML export.
	HTMLToMarkdown func(html string) string
}

// FetchCanvasMarkdown downloads a canvas's HTML export and converts it to
// Markdown.
func FetchCanvasMarkdown(ctx context.Context, c *Client, canvasID string, opts CanvasOptions) (Canvas, error) {
	maxChars := opts.MaxChars
	if maxChars == 0 {
		maxChars = 20000
	}

	info, err := c.API(ctx, "files.info", map[string]any{"file": canvasID})
	if err != nil {
		return Canvas{}, err
	}
	file := getRec(info, "file")
	if file == nil {
		return Canvas{}, agenterrors.New("canvas not found (files.info returned no file)", agenterrors.FixableByAgent)
	}
	title := strings.TrimSpace(FirstNonEmpty(getStr(file, "title"), getStr(file, "name")))
	downloadURL := FirstNonEmpty(getStr(file, "url_private_download"), getStr(file, "url_private"))
	if downloadURL == "" {
		return Canvas{}, agenterrors.New("canvas has no download URL", agenterrors.FixableByAgent)
	}

	htmlPath, err := c.DownloadFile(ctx, DownloadOptions{
		URL:           downloadURL,
		DestDir:       opts.DownloadsDir,
		PreferredName: canvasID + ".html",
		AllowHTML:     true,
	})
	if err != nil {
		return Canvas{}, agenterrors.Wrap(err, agenterrors.FixableByRetry).
			WithHint("canvas download failed — credentials may need a refresh")
	}
	htmlBytes, err := os.ReadFile(htmlPath)
	if err != nil {
		return Canvas{}, agenterrors.Wrap(err, agenterrors.FixableByRetry)
	}
	if authPageRe.Match(htmlBytes) {
		return Canvas{}, agenterrors.New("downloaded auth/login page instead of canvas content (token may be expired)", agenterrors.FixableByHuman).
			WithHint(reimportHint)
	}

	markdown := render.TruncateBody(strings.TrimSpace(opts.HTMLToMarkdown(string(htmlBytes))), maxChars)
	return Canvas{ID: canvasID, Title: title, Markdown: markdown}, nil
}
