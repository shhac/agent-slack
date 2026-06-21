---
description: Release via tag push — CI builds, publishes, and bumps the Homebrew formula
argument-hint: <patch|minor|major>
---

# Release

Releasing `agent-slack` is automated. Pushing a `v*` tag triggers
`.github/workflows/release.yml`, which calls the shared `go-release` workflow in
`shhac/homebrew-tap` to cross-build every platform, publish the GitHub Release,
and regenerate + push `Formula/agent-slack.rb` (with shell completions) to the tap.
**No manual build, and no manual formula bump.**

## Steps

1. `$ARGUMENTS` must be `patch`, `minor`, or `major` — else stop and ask.
2. Pre-flight (CI re-runs tests on the tag, but check locally first):
   - Clean tree (`git status --short`), on `main`, up to date with `origin/main`.
   - Tests, vet, and lint pass (e.g. `make test` / `go test ./...`, `go vet ./...`,
     `make lint` / `golangci-lint run ./...`). The version is injected from the tag
     (`-ldflags -X main.version=…`) — there is no version file to edit.
3. Compute the new version by bumping the latest tag
   (`git describe --tags --abbrev=0`): patch → x.y.(z+1), minor → x.(y+1).0,
   major → (x+1).0.0.
4. Tag and push — this is the whole release:
   ```bash
   git tag "v${new_version}"
   git push origin "v${new_version}"
   ```
5. Verify CI and the outputs:
   ```bash
   gh run watch --repo shhac/agent-slack          # both jobs green: build+release, homebrew tap
   gh release view "v${new_version}" --repo shhac/agent-slack   # 6 assets
   ```
   Install / upgrade: `brew install shhac/tap/agent-slack` · `brew upgrade shhac/tap/agent-slack`

## Manual fallback (only if the workflow itself is broken)

Re-run a failed release with `gh run rerun <id> --repo shhac/agent-slack`. To bypass
the workflow entirely, build the `GOOS/GOARCH` binaries with
`-ldflags "-s -w -X main.version=<v>"`, `gh release create` the tarballs, and edit
`Formula/agent-slack.rb` by hand (see this file's git history for the old full flow).
