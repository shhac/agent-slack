package slack

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/shhac/agent-slack/internal/render"
)

// DownloadError carries the HTTP status of a failed file download so callers
// can report it without aborting the whole command.
type DownloadError struct {
	Message string
	Status  int
}

func (e *DownloadError) Error() string { return e.Message }

// DownloadOptions controls Client.DownloadFile.
type DownloadOptions struct {
	URL           string
	DestDir       string
	PreferredName string
	// AllowHTML permits text/html responses (canvases download as HTML).
	// Otherwise an HTML body means Slack served a login page — auth failed.
	AllowHTML bool
}

var unsafeFilenameRe = regexp.MustCompile(`[\\/<>:"|?*]`)

func sanitizeFilename(name string) string {
	cleaned := unsafeFilenameRe.ReplaceAllString(name, "_")
	// "." and ".." survive the character filter but would escape the dest
	// dir when joined onto a path.
	if cleaned == "." || cleaned == ".." {
		return "_"
	}
	return cleaned
}

// DownloadFile fetches a Slack-hosted file to DestDir with the account's
// credentials and returns the local path. Existing files are reused (file IDs
// are immutable, so the cache never goes stale).
func (c *Client) DownloadFile(ctx context.Context, opts DownloadOptions) (string, error) {
	absDir, err := filepath.Abs(opts.DestDir)
	if err != nil {
		return "", &DownloadError{Message: err.Error()}
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return "", &DownloadError{Message: err.Error()}
	}
	name := opts.PreferredName
	if name == "" {
		if u, perr := url.Parse(opts.URL); perr == nil {
			name = filepath.Base(u.Path)
		}
	}
	if name == "" || name == "." || name == "/" {
		name = "file"
	}
	path := filepath.Join(absDir, sanitizeFilename(name))

	if _, err := os.Stat(path); err == nil {
		return path, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return "", &DownloadError{Message: err.Error()}
	}
	auth := c.currentAuth()
	if auth.Type == AuthBrowser {
		req.Header.Set("Authorization", "Bearer "+auth.XOXC)
		req.Header.Set("Cookie", "d="+url.QueryEscape(auth.XOXD))
		req.Header.Set("Referer", "https://app.slack.com/")
		req.Header.Set("User-Agent", c.userAgent)
	} else {
		req.Header.Set("Authorization", "Bearer "+auth.Token)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return "", &DownloadError{Message: "network error: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &DownloadError{Message: fmt.Sprintf("failed to download file (%d)", resp.StatusCode), Status: resp.StatusCode}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", &DownloadError{Message: "failed to read download response body: " + err.Error(), Status: resp.StatusCode}
	}
	if !opts.AllowHTML && strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		head := string(body)
		if len(head) > 120 {
			head = head[:120]
		}
		return "", &DownloadError{
			Message: fmt.Sprintf("downloaded HTML instead of file (auth likely failed). First bytes: %q", head),
			Status:  resp.StatusCode,
		}
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", &DownloadError{Message: err.Error()}
	}
	return path, nil
}

// WriteDownloadErrorFile records a failed download next to where the file
// would have been, so the output's path always points at something readable.
func WriteDownloadErrorFile(destDir, fileID, errMsg string) string {
	absDir, err := filepath.Abs(destDir)
	if err != nil {
		return ""
	}
	if err := os.MkdirAll(absDir, 0o700); err != nil {
		return ""
	}
	path := filepath.Join(absDir, sanitizeFilename(fileID+".download-error.txt"))
	if err := os.WriteFile(path, []byte(errMsg+"\n"), 0o600); err != nil {
		return ""
	}
	return path
}

// InferFileExt picks a filename extension from mimetype/filetype, falling
// back to the original filename's extension.
func InferFileExt(f render.FileSummary) string {
	mt := strings.ToLower(f.Mimetype)
	ft := strings.ToLower(f.Filetype)
	switch {
	case mt == "image/png" || ft == "png":
		return "png"
	case mt == "image/jpeg" || mt == "image/jpg" || ft == "jpg" || ft == "jpeg":
		return "jpg"
	case mt == "image/webp" || ft == "webp":
		return "webp"
	case mt == "image/gif" || ft == "gif":
		return "gif"
	case mt == "text/plain" || ft == "text":
		return "txt"
	case mt == "text/markdown" || ft == "markdown" || ft == "md":
		return "md"
	case mt == "application/json" || ft == "json":
		return "json"
	}
	name := f.Name
	if name == "" {
		name = f.Title
	}
	if m := fileExtRe.FindStringSubmatch(name); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

var (
	fileExtRe   = regexp.MustCompile(`\.([A-Za-z0-9]{1,10})$`)
	authPageRe  = regexp.MustCompile(`(?i)<form[^>]+signin|data-qa="signin|<title>[^<]*Sign\s*in`)
	canvasModes = map[string]bool{"canvas": true, "quip": true, "docs": true}
)

// MessageDownloads configures DownloadMessageFiles.
type MessageDownloads struct {
	DestDir string
	// CanvasMarkdown converts canvas-export HTML to Markdown. Required for
	// canvas/quip/docs files; the CLI wires internal/htmlmd.
	CanvasMarkdown func(html string) string
	// Warn receives non-fatal per-file failure notices (defaults to discard).
	Warn io.Writer
}

// DownloadMessageFiles fetches every file referenced by the messages into
// DestDir, keyed by file ID. Canvas-mode files are converted to Markdown.
// Failures never abort: they surface as error entries pointing at a
// .download-error.txt.
func DownloadMessageFiles(ctx context.Context, c *Client, messages []render.MessageSummary, opts MessageDownloads) map[string]render.DownloadResult {
	warn := opts.Warn
	if warn == nil {
		warn = io.Discard
	}
	out := map[string]render.DownloadResult{}
	for _, message := range messages {
		for _, file := range message.Files {
			if _, done := out[file.ID]; done {
				continue
			}
			isCanvas := canvasModes[file.Mode]

			// Canvases must use url_private (the download variant 404s);
			// everything else prefers the download URL.
			fileURL := file.URLPrivateDownload
			if isCanvas {
				fileURL = file.URLPrivate
				if fileURL == "" {
					fileURL = file.URLPrivateDownload
				}
			} else if fileURL == "" {
				fileURL = file.URLPrivate
			}
			if fileURL == "" {
				continue
			}

			var path string
			var err error
			if isCanvas {
				path, err = c.downloadCanvasAsMarkdown(ctx, file.ID, fileURL, opts)
			} else {
				name := file.ID
				if ext := InferFileExt(file); ext != "" {
					name += "." + ext
				}
				path, err = c.DownloadFile(ctx, DownloadOptions{URL: fileURL, DestDir: opts.DestDir, PreferredName: name})
			}

			if err != nil {
				errPath := WriteDownloadErrorFile(opts.DestDir, file.ID, err.Error())
				out[file.ID] = render.DownloadResult{OK: false, Error: err.Error(), Path: errPath}
				_, _ = fmt.Fprintf(warn, "Warning: skipping file %s: %s\n", file.ID, err.Error())
				continue
			}
			out[file.ID] = render.DownloadResult{OK: true, Path: path}
		}
	}
	return out
}

func (c *Client) downloadCanvasAsMarkdown(ctx context.Context, fileID, fileURL string, opts MessageDownloads) (string, error) {
	if opts.CanvasMarkdown == nil {
		return "", &DownloadError{Message: "no canvas converter configured"}
	}
	htmlPath, err := c.DownloadFile(ctx, DownloadOptions{
		URL:           fileURL,
		DestDir:       opts.DestDir,
		PreferredName: fileID + ".html",
		AllowHTML:     true,
	})
	if err != nil {
		return "", err
	}
	html, err := os.ReadFile(htmlPath)
	if err != nil {
		return "", &DownloadError{Message: err.Error()}
	}
	if authPageRe.Match(html) {
		return "", &DownloadError{Message: "downloaded auth/login page instead of canvas content (token may be expired)"}
	}
	markdown := strings.TrimSpace(opts.CanvasMarkdown(string(html)))
	mdPath := filepath.Join(opts.DestDir, sanitizeFilename(fileID)+".md")
	if err := os.WriteFile(mdPath, []byte(markdown), 0o600); err != nil {
		return "", &DownloadError{Message: err.Error()}
	}
	return mdPath, nil
}
