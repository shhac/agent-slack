package slack

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
)

// Custom-emoji mutations go through the same internal endpoints the Slack web
// client uses (verified from a browser HAR): emoji.add (multipart, with an
// image file part for an upload or mode=alias for an alias) and emoji.remove
// (multipart, name only). Both need a user/browser (xoxc) token — there is no
// public bot scope for them; a bot token yields a Slack error we surface as-is.
// After a successful mutation the cached set is dropped so the next list/get
// re-fetches.

// emojiNameRe matches a custom emoji name (lowercase): letters, digits, and
// _ - + '. Slack lowercases names, so we do too before validating.
var emojiNameRe = regexp.MustCompile(`^[a-z0-9_'+-]+$`)

func normalizeEmojiName(input string) (string, error) {
	name := strings.ToLower(strings.Trim(strings.TrimSpace(input), ":"))
	if name == "" {
		return "", agenterrors.New("emoji name is empty", agenterrors.FixableByAgent)
	}
	if !emojiNameRe.MatchString(name) {
		return "", agenterrors.Newf(agenterrors.FixableByAgent,
			"invalid emoji name %q: use lowercase letters, digits, and _ - + '", input)
	}
	return name, nil
}

// AddEmoji uploads imagePath as a new custom emoji named name. Returns the
// stored (lowercased) name.
func AddEmoji(ctx context.Context, c *Client, name, imagePath string) (string, error) {
	clean, err := normalizeEmojiName(name)
	if err != nil {
		return "", err
	}
	data, contentType, err := readEmojiImage(imagePath)
	if err != nil {
		return "", err
	}
	params := map[string]any{"name": clean, "mode": "data"}
	if _, err := c.APIMultipartFile(ctx, "emoji.add", params, "image", filepath.Base(imagePath), contentType, data); err != nil {
		return "", err
	}
	c.forgetEmojiCache()
	return clean, nil
}

// AddEmojiAlias creates an alias name pointing at an existing emoji target
// (custom or standard). NOTE: the alias mode (mode=alias, alias_for) is the
// documented Slack web shape but was not in the captured HAR — if Slack rejects
// it, capture an alias-add HAR to confirm the field names.
func AddEmojiAlias(ctx context.Context, c *Client, name, target string) (string, error) {
	clean, err := normalizeEmojiName(name)
	if err != nil {
		return "", err
	}
	aliasFor, err := normalizeEmojiName(target)
	if err != nil {
		return "", err
	}
	params := map[string]any{"name": clean, "mode": "alias", "alias_for": aliasFor}
	if _, err := c.APIMultipart(ctx, "emoji.add", params); err != nil {
		return "", err
	}
	c.forgetEmojiCache()
	return clean, nil
}

// RemoveEmoji deletes a custom emoji by name.
func RemoveEmoji(ctx context.Context, c *Client, name string) (string, error) {
	clean, err := normalizeEmojiName(name)
	if err != nil {
		return "", err
	}
	if _, err := c.APIMultipart(ctx, "emoji.remove", map[string]any{"name": clean}); err != nil {
		return "", err
	}
	c.forgetEmojiCache()
	return clean, nil
}

var emojiImageTypes = map[string]bool{
	"image/png":  true,
	"image/gif":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// readEmojiImage loads a local image and reports its content type, sniffed from
// the bytes (the reliable signal) and validated against the types Slack accepts.
func readEmojiImage(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", agenterrors.Newf(agenterrors.FixableByAgent, "cannot read image %q: %v", path, err).
			WithHint("pass a path to a local png, gif, jpeg, or webp image")
	}
	if len(data) == 0 {
		return nil, "", agenterrors.Newf(agenterrors.FixableByAgent, "image %q is empty", path)
	}
	contentType := http.DetectContentType(data)
	if !emojiImageTypes[contentType] {
		return nil, "", agenterrors.Newf(agenterrors.FixableByAgent,
			"unsupported image type %q for %q", contentType, path).
			WithHint("Slack custom emoji must be png, gif, jpeg, or webp")
	}
	return data, contentType, nil
}
