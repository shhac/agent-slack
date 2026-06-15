package slack

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

const maxUploadBytes = 100 * 1024 * 1024 // Slack's upload limit

// UploadLocalFiles pushes one or more local files to a channel and posts them
// as a SINGLE message: each file's bytes go up via Slack's external upload flow
// (files.getUploadURLExternal → raw POST), then one files.completeUploadExternal
// lists every file_id. So multiple attachments share one message and one
// initial_comment, instead of fanning out to a message per file.
func (c *Client) UploadLocalFiles(ctx context.Context, channelID string, filePaths []string, threadTS, initialComment string) error {
	files := make([]any, 0, len(filePaths))
	for _, path := range filePaths {
		fileID, filename, err := c.uploadFileBytes(ctx, path)
		if err != nil {
			return err
		}
		files = append(files, map[string]any{"id": fileID, "title": filename})
	}
	if len(files) == 0 {
		return nil
	}

	completeParams := map[string]any{"files": files, "channel_id": channelID}
	if threadTS != "" {
		completeParams["thread_ts"] = threadTS
	}
	if initialComment != "" {
		completeParams["initial_comment"] = initialComment
	}
	_, err := c.API(ctx, "files.completeUploadExternal", completeParams)
	return err
}

// UploadDraftFiles uploads each local file's bytes, finalizes them, and returns
// the resulting file ids ready to attach to a draft. The byte uploads run in
// parallel, then a single files.completeUploadExternal with NO channel_id
// finalizes them: that turns the pending uploads into real files WITHOUT
// posting them anywhere. The completion is the load-bearing step — drafts.create
// rejects a still-pending file_id with file_not_found (it races Slack
// registering the upload), but reliably accepts a completed one.
func (c *Client) UploadDraftFiles(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	type uploaded struct {
		id, filename string
		err          error
	}
	results := make([]uploaded, len(paths))
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			id, filename, err := c.uploadFileBytes(ctx, path)
			results[i] = uploaded{id: id, filename: filename, err: err}
		}(i, path)
	}
	wg.Wait()

	files := make([]any, 0, len(paths))
	ids := make([]string, 0, len(paths))
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		files = append(files, map[string]any{"id": r.id, "title": r.filename})
		ids = append(ids, r.id)
	}
	// Finalize without a channel: real files, no message posted.
	if _, err := c.API(ctx, "files.completeUploadExternal", map[string]any{"files": files}); err != nil {
		return nil, err
	}
	return ids, nil
}

// uploadFileBytes validates a local file and uploads its bytes to the
// per-file URL Slack hands out, returning the file_id to complete with and the
// base filename (its title). It does not post anything — the caller batches the
// completion so several files can land on one message.
func (c *Client) uploadFileBytes(ctx context.Context, filePath string) (fileID, filename string, err error) {
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return "", "", agenterrors.Newf(agenterrors.FixableByAgent, "cannot read attachment %q: %v", filePath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", agenterrors.Newf(agenterrors.FixableByAgent, "cannot read attachment %q: %v", filePath, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", agenterrors.Newf(agenterrors.FixableByAgent, "attachment path is not a file: %s", filePath)
	}
	if info.Size() > maxUploadBytes {
		return "", "", agenterrors.Newf(agenterrors.FixableByAgent,
			"file too large (%dMB); Slack allows up to 100MB", info.Size()/1024/1024)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", agenterrors.Newf(agenterrors.FixableByAgent, "cannot read attachment %q: %v", filePath, err)
	}
	filename = filepath.Base(resolved)

	initResp, err := c.API(ctx, "files.getUploadURLExternal", map[string]any{
		"filename": filename,
		"length":   len(data),
	})
	if err != nil {
		return "", "", err
	}
	uploadURL := getStr(initResp, "upload_url")
	fileID = getStr(initResp, "file_id")
	if uploadURL == "" || fileID == "" {
		return "", "", agenterrors.New("Slack did not return an upload URL for the file attachment", agenterrors.FixableByRetry)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(data))
	if err != nil {
		return "", "", mapNetworkError("files upload", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.doer.Do(req)
	if err != nil {
		return "", "", mapNetworkError("files upload", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", "", agenterrors.New(fmt.Sprintf("failed to upload attachment bytes (HTTP %d)", resp.StatusCode), agenterrors.FixableByRetry)
	}
	return fileID, filename, nil
}
