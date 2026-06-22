package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/shhac/agent-slack/internal/render"
)

// searchHit downloads (when enabled), compacts, and content-type-filters one
// matched message. ok=false means the content-type filter rejected it.
// thread_ts is dropped from hits: the permalink or channel_id+ts chain into
// message get/list.
func searchHit(ctx context.Context, c *Client, opts SearchOptions, summary render.MessageSummary, downloaded map[string]render.DownloadResult, permalink string) (SearchMessageItem, bool) {
	if opts.Download {
		for id, res := range DownloadMessageFiles(ctx, c, []render.MessageSummary{summary}, MessageDownloads{DestDir: opts.DownloadsDir, Warn: opts.Warn}) {
			downloaded[id] = res
		}
	}
	compact := render.ToCompactMessage(summary, render.CompactOptions{MaxBodyChars: opts.MaxContentChars, DownloadedPaths: downloaded, SlackMarkdown: opts.SlackMarkdown})
	if !PassesContentTypeFilter(compact, opts.ContentType) {
		return SearchMessageItem{}, false
	}
	compact.ThreadTS = ""
	return SearchMessageItem{CompactMessage: compact, Permalink: permalink}, true
}

func downloadSearchFile(ctx context.Context, c *Client, f map[string]any, opts SearchOptions) (SearchFileItem, bool) {
	mode := getStr(f, "mode")
	mimetype := getStr(f, "mimetype")
	if !passesFileContentTypeFilter(mode, mimetype, opts.ContentType) {
		return SearchFileItem{}, false
	}
	fileURL := FirstNonEmpty(getStr(f, "url_private_download"), getStr(f, "url_private"))
	id := getStr(f, "id")
	if fileURL == "" || id == "" {
		return SearchFileItem{}, false
	}
	name := id
	if ext := InferFileExt(render.FileSummary{
		Mimetype: mimetype, Filetype: getStr(f, "filetype"),
		Name: getStr(f, "name"), Title: getStr(f, "title"),
	}); ext != "" {
		name += "." + ext
	}
	path, err := c.DownloadFile(ctx, DownloadOptions{URL: fileURL, DestDir: opts.DownloadsDir, PreferredName: name})
	if err != nil {
		_, _ = fmt.Fprintf(opts.Warn, "Warning: skipping file %s: %s\n", id, err.Error())
		return SearchFileItem{}, false
	}
	return SearchFileItem{
		Title:    strings.TrimSpace(FirstNonEmpty(getStr(f, "title"), getStr(f, "name"))),
		Mimetype: mimetype,
		Mode:     mode,
		Path:     path,
	}, true
}

// PassesContentTypeFilter classifies a compact message by its files.
func PassesContentTypeFilter(m render.CompactMessage, contentType ContentType) bool {
	if contentType == ContentAny {
		return true
	}
	hasFiles := len(m.Files) > 0
	if contentType == ContentText {
		return !hasFiles
	}
	if !hasFiles {
		return false
	}
	switch contentType {
	case ContentFile:
		return true
	case ContentSnippet:
		for _, f := range m.Files {
			if f.Mode == "snippet" {
				return true
			}
		}
		return false
	case ContentImage:
		for _, f := range m.Files {
			if strings.HasPrefix(f.Mimetype, "image/") {
				return true
			}
		}
		return false
	}
	return true
}

func passesFileContentTypeFilter(mode, mimetype string, contentType ContentType) bool {
	switch contentType {
	case ContentAny, ContentFile:
		return true
	case ContentSnippet:
		return mode == "snippet"
	case ContentImage:
		return strings.HasPrefix(strings.ToLower(mimetype), "image/")
	case ContentText:
		return mimetype == "text/plain"
	}
	return true
}
