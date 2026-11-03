package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	agenterrors "github.com/shhac/agent-slack/internal/errors"
	"github.com/shhac/agent-slack/internal/render"
	"github.com/shhac/agent-slack/internal/slack"
)

// uploadInlineImages uploads the local file behind every `![alt](path)` image
// block and points the block at it, in place. Rendering only records the path;
// this is the I/O half. Posting a block that references the file shares it
// into that conversation, so recipients can see it.
//
// A path inside message text is a quieter channel than --attach, so only real
// images go up: anything else is refused before a byte leaves the machine.
func uploadInlineImages(ctx context.Context, c *slack.Client, blocks []any) error {
	type pending struct{ block, file int }
	var waiting []pending
	var paths []string
	seen := map[string]int{}
	for i, b := range blocks {
		img, ok := b.(render.ImageBlock)
		if !ok || img.LocalPath == "" {
			continue
		}
		path, err := localImagePath(img.LocalPath)
		if err != nil {
			return err
		}
		file, ok := seen[path]
		if !ok {
			file = len(paths)
			seen[path] = file
			paths = append(paths, path)
		}
		waiting = append(waiting, pending{block: i, file: file})
	}
	if len(paths) == 0 {
		return nil
	}

	ids, err := c.UploadUnsharedFiles(ctx, paths)
	if err != nil {
		return err
	}
	if err := c.WaitForImages(ctx, ids); err != nil {
		return err
	}
	for _, p := range waiting {
		img, _ := blocks[p.block].(render.ImageBlock)
		img.SlackFile = &render.SlackFile{ID: ids[p.file]}
		blocks[p.block] = img
	}
	return nil
}

// localImagePath resolves an image source to a readable image file. `~/` is
// expanded because the text never passed through a shell that would.
func localImagePath(src string) (string, error) {
	path := src
	if rest, ok := strings.CutPrefix(src, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", agenterrors.Newf(agenterrors.FixableByAgent, "image %q: cannot resolve ~: %v", src, err)
		}
		path = filepath.Join(home, rest)
	}
	kind, err := sniffContentType(path)
	if err != nil {
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "image %q: %v", src, err).
			WithHint("![alt](path) uploads a local image; use an existing path, an https:// URL, or a Slack file id (F…)")
	}
	if !strings.HasPrefix(kind, "image/") {
		return "", agenterrors.Newf(agenterrors.FixableByAgent, "image %q is not an image (looks like %s)", src, kind).
			WithHint("only PNG, JPEG, GIF, WebP or BMP files can be inlined; send other files with --attach")
	}
	return path, nil
}

func sniffContentType(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && n == 0 {
		return "", err
	}
	return http.DetectContentType(head[:n]), nil
}
