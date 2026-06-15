package slack

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

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

// UploadDraftFiles uploads each local file's bytes and returns the resulting
// file ids, ready to attach to a draft (drafts.create/update reference ids
// directly). Unlike UploadLocalFiles it does NOT call completeUploadExternal —
// that would post a message; a draft just holds the ids until the human sends.
func (c *Client) UploadDraftFiles(ctx context.Context, paths []string) ([]string, error) {
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		fileID, _, err := c.uploadFileBytes(ctx, path)
		if err != nil {
			return nil, err
		}
		ids = append(ids, fileID)
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
