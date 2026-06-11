---
description: Build, release, and publish to Homebrew
argument-hint: <patch|minor|major>
---

# Release

Perform a full release of the `agent-slack` CLI: version bump, tag, build,
GitHub release, and Homebrew tap update.

## Arguments

- `$ARGUMENTS` - version bump type: `patch`, `minor`, or `major`

## Instructions

You are performing a release of the `agent-slack` CLI (Go). Follow these steps
exactly.

### Pre-flight

1. Confirm `$ARGUMENTS` is exactly `patch`, `minor`, or `major`. If not, stop and ask.
2. Confirm the working tree is clean:
   ```bash
   git status --short
   ```
   If there are changes, stop and ask.
3. Confirm the current branch is `main`, a GitHub remote named `origin` exists
   (`git remote -v` — this repo may not be pushed yet; if there is no remote,
   stop and ask the user to create the GitHub repo and add it), and `main` is
   up to date with `origin/main`.
4. Run the full verification (matches the repo's Makefile and the family
   convention):
   ```bash
   make test
   make vet
   make lint
   GOOS=windows go build ./...
   ```
   `agent-slack` supports Windows credential import (DPAPI), so the
   cross-build must stay green. If any command fails, stop and fix.
5. Determine the current version from the latest git tag:
   ```bash
   current=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "0.0.0")
   ```

### Step 1: Version bump, tag, and push

Calculate the next version:

```bash
current=$(git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "0.0.0")
IFS='.' read -r major minor patch <<< "$current"

case "$ARGUMENTS" in
  patch) patch=$((patch + 1)) ;;
  minor) minor=$((minor + 1)); patch=0 ;;
  major) major=$((major + 1)); minor=0; patch=0 ;;
  *) echo "expected patch, minor, or major"; exit 1 ;;
esac

new_version="${major}.${minor}.${patch}"
echo "Releasing v${new_version}"
```

Then tag and push:

```bash
git tag "v${new_version}"
git push origin main "v${new_version}"
```

### Step 2: Build manually

Releases for this repo are manual (no GoReleaser). The version is injected via
`-X main.version`, matching `cmd/agent-slack/main.go`.

```bash
rm -rf dist/
mkdir -p dist
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -X main.version=${new_version}" -o "dist/agent-slack-darwin-arm64" ./cmd/agent-slack
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X main.version=${new_version}" -o "dist/agent-slack-darwin-amd64" ./cmd/agent-slack
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=${new_version}" -o "dist/agent-slack-linux-amd64" ./cmd/agent-slack
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w -X main.version=${new_version}" -o "dist/agent-slack-linux-arm64" ./cmd/agent-slack
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.version=${new_version}" -o "dist/agent-slack-windows-amd64.exe" ./cmd/agent-slack

cd dist
for bin in agent-slack-darwin-arm64 agent-slack-darwin-amd64 agent-slack-linux-amd64 agent-slack-linux-arm64; do
  tar czf "${bin}.tar.gz" "$bin"
done
zip agent-slack-windows-amd64.zip agent-slack-windows-amd64.exe
shasum -a 256 *.tar.gz *.zip > checksums-sha256.txt
cd ..
```

Smoke-test the native binary:

```bash
./dist/agent-slack-darwin-arm64 --version
./dist/agent-slack-darwin-arm64 usage
```

### Step 3: Create GitHub release manually

```bash
prev_tag=$(git tag --sort=-v:refname | head -2 | tail -1)
notes=$(git log --pretty=format:"- %s" "${prev_tag}..v${new_version}" --no-merges | grep -v "^- v[0-9]" || true)

gh release create "v${new_version}" dist/*.tar.gz dist/*.zip dist/checksums-sha256.txt \
  --title "v${new_version}" \
  --notes "$notes"
```

### Step 4: Update Homebrew tap

The Homebrew formula lives in `../homebrew-tap` relative to this repo root.
Create or update `../homebrew-tap/Formula/agent-slack.rb` using the sibling
agent formula pattern (`../homebrew-tap/Formula/agent-sql.rb` is the reference),
with:

- Class name: `AgentSlack`
- desc: `"Slack CLI for AI agents"`
- homepage: `https://github.com/shhac/agent-slack`
- version, URLs, and SHA256 values from `dist/checksums-sha256.txt`
- test assertions for `agent-slack --version` and `agent-slack usage`

Then commit and push the tap (this repo lives outside `~/projects/`, so plain
git only — no Graphite):

```bash
cd ../homebrew-tap
git status --short
git hunk add --all --file Formula/agent-slack.rb
git commit -m "agent-slack ${new_version}"
git push
cd -
```

Always return to the `agent-slack` repo after updating the tap.

### Step 5: Report

Show the user:

- New version number
- GitHub release URL
- Homebrew tap commit, if applicable
- `brew install shhac/tap/agent-slack`
- `brew upgrade shhac/tap/agent-slack`
