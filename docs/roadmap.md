# Roadmap

Intent, not a schedule. Kept short on purpose — this project has already been
through one plan that grew to 85 tasks and shipped none of them.

## 1. Release automation and repository safeguards

Publishing a tag by hand works exactly once. The goal is that `git tag vX.Y.Z`
produces attached, reproducible binaries without further intervention.

- A `release` workflow triggered on `v*` tags: `CGO_ENABLED=0` cross-compilation
  for linux/darwin × amd64/arm64, `VERSION` injected through the existing
  `-ldflags` path, checksums, and a GitHub Release created from the tag message.
- Branch protection on `main`: require the existing `check` job, require a PR.
- Dependabot or equivalent for Go module updates.
- Verify the released binary reports the tag it was built from. This has failed
  silently before — the Makefile's `-ldflags` named a stale module path after
  the module rename, and every build reported `dev` until it was noticed.

## 2. Documentation that shows the output

The README describes what a report contains but never shows one, and the report
is the most persuasive thing Clogs produces.

- Commit a sample incident report generated entirely from `testdata/`, and link
  it from the README. It is a self-contained HTML file with no external assets,
  so it can be served from GitHub Pages and viewed without cloning.
- A screenshot in the README for readers who will never click through.
- Expand the shipped fixtures so the sample report exercises the sections that
  currently have nothing interesting to show — route impact, the site heatmap,
  recurring signatures.

Everything published this way must come from invented data. See
`testdata/README.md`.

## 3. A public target for trying `remote fetch`

The idea: host a directory of dummy Catalina and access logs somewhere public,
so a new user can run a real fetch before pointing Clogs at their own servers.

**This does not work as currently designed, and the reason is worth recording.**
`clogs remote fetch` delegates to OpenSSH — it shells out to `ssh` and `sftp`
and never speaks HTTP. That is a deliberate security decision (Clogs never
handles credentials) and not something to trade away for a demo. GitHub, a CDN,
or object storage can serve files over HTTPS, but none of them offer the
anonymous SFTP endpoint this would require.

Three ways forward, in increasing order of cost:

1. **Ship the sample collection as a release asset.** A user runs `curl` and
   then `clogs ingest`. It demonstrates everything after acquisition and needs
   no new code. Least impressive, entirely honest.
2. **Add an HTTP source to `internal/download`.** A real feature with real
   value — logs are not always behind SSH — but it widens the security surface
   that OpenSSH delegation was chosen to keep narrow. Worth doing only if it is
   wanted for its own sake, never merely to enable a demo.
3. **Run an anonymous SFTP endpoint.** Closest to the real experience and the
   only option that exercises the actual code path, at the cost of operating an
   internet-facing service indefinitely.

Option 1 is the recommended starting point. It gets a newcomer to a rendered
report in one command, which is the actual goal.
