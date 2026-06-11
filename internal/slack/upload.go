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

// UploadLocalFile pushes one local file to a channel via Slack's external
// upload flow (getUploadURLExternal → raw POST → completeUploadExternal).
func (c *Client) UploadLocalFile(ctx context.Context, channelID, filePath, threadTS, initialComment string) error {
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		return agenterrors.Newf(agenterrors.FixableByAgent, "cannot read attachment %q: %v", filePath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return agenterrors.Newf(agenterrors.FixableByAgent, "cannot read attachment %q: %v", filePath, err)
	}
	if !info.Mode().IsRegular() {
		return agenterrors.Newf(agenterrors.FixableByAgent, "attachment path is not a file: %s", filePath)
	}
	if info.Size() > maxUploadBytes {
		return agenterrors.Newf(agenterrors.FixableByAgent,
			"file too large (%dMB); Slack allows up to 100MB", info.Size()/1024/1024)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return agenterrors.Newf(agenterrors.FixableByAgent, "cannot read attachment %q: %v", filePath, err)
	}
	filename := filepath.Base(resolved)

	initResp, err := c.API(ctx, "files.getUploadURLExternal", map[string]any{
		"filename": filename,
		"length":   len(data),
	})
	if err != nil {
		return err
	}
	uploadURL := getStr(initResp, "upload_url")
	fileID := getStr(initResp, "file_id")
	if uploadURL == "" || fileID == "" {
		return agenterrors.New("Slack did not return an upload URL for the file attachment", agenterrors.FixableByRetry)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(data))
	if err != nil {
		return mapNetworkError("files upload", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := c.doer.Do(req)
	if err != nil {
		return mapNetworkError("files upload", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return agenterrors.New(fmt.Sprintf("failed to upload attachment bytes (HTTP %d)", resp.StatusCode), agenterrors.FixableByRetry)
	}

	completeParams := map[string]any{
		"files":      []any{map[string]any{"id": fileID, "title": filename}},
		"channel_id": channelID,
	}
	if threadTS != "" {
		completeParams["thread_ts"] = threadTS
	}
	if comment := initialComment; comment != "" {
		completeParams["initial_comment"] = comment
	}
	_, err = c.API(ctx, "files.completeUploadExternal", completeParams)
	return err
}
