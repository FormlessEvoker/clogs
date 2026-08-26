# Releasing

Every change reaches `main` through a pull request, and every merge into `main`
publishes a release. The version number is not typed by hand anywhere — it is
derived from a label on the pull request, which is why that label is a merge
requirement rather than a convention.

## The flow

1. Branch, commit, open a pull request against `main`.
2. Label it `major`, `minor`, or `patch`. Exactly one.
3. CI runs three required checks: `check`, `cross-build`, and `semver-label`.
4. Approve the pull request. Approval arms GitHub's auto merge, so it merges
   itself as soon as the checks are green — nobody has to come back and click.
5. The merge triggers `release.yml`, which reads the label, computes the next
   tag, builds every target, pushes the tag, and publishes a GitHub release with
   the archives attached.

Nothing between steps 4 and 5 needs a human.

## Choosing the label

| Label | `v1.4.2` becomes | Use it for |
| --- | --- | --- |
| `major` | `v2.0.0` | a breaking change to the CLI surface, config schema, or output format |
| `minor` | `v1.5.0` | a new command, flag, or log format |
| `patch` | `v1.4.3` | a fix, a documentation change, an internal refactor |

Output format counts. Tests assert on exact bytes, and so do the people piping
`clogs query` into something else.

## The checks

| Check | What it guards |
| --- | --- |
| `check` | `make check` — formatting, `go vet`, tests, race tests, the prohibited-content gate |
| `cross-build` | every published release target still compiles |
| `semver-label` | exactly one of `major`, `minor`, `patch` is present |

`cross-build` exists because a release that fails halfway has already moved the
version tag. A target that stops building — most likely because
`modernc.org/sqlite` does not support it — should fail on the pull request
instead.

## What the release job produces

`scripts/build-release.sh` compiles five targets from one Linux runner. The
SQLite driver is pure Go, so `CGO_ENABLED=0` cross-compiles all of them with no
C toolchain and no per-platform runner:

- `darwin/arm64` — Apple silicon
- `darwin/amd64` — Intel Mac
- `windows/amd64`
- `linux/amd64`
- `linux/arm64`

Each becomes a flat archive named `clogs_<version>_<os>_<arch>.tar.gz`, or
`.zip` on Windows, holding the binary, `README.md`, and `LICENSE`. A
`checksums.txt` with the SHA-256 of every archive is attached alongside them.

The version is injected at link time into
`internal/cli.BuildVersion`, so `clogs version` on a downloaded binary prints
the tag it was cut from.

Build the whole set locally to see exactly what a release will contain:

```sh
make release-build VERSION=v9.9.9
ls bin/dist
```

`bin/` is ignored by git and skipped by the prohibited-content gate, so build
products can never reach a commit or trip a scan.

### Adding a target

Add the `os/arch` pair to the default list in `scripts/build-release.sh`. The
`cross-build` check picks it up on the next pull request, which is where you
will find out whether the SQLite driver supports it.

Note that a Windows build compiles and runs, but `clogs fetch` and
`clogs remote` will not work there: they shell out to OpenSSH and use
ControlMaster multiplexing over a Unix socket.

## Repository settings this depends on

These live in GitHub settings, not in the repository, so they have to be applied
by hand once:

- Labels `major`, `minor`, and `patch` exist.
- Settings → General → Pull Requests → **Allow auto-merge** is enabled.
  Without it, step 4 above cannot arm and the workflow falls back to merging
  immediately.
- A ruleset on `main` that restricts deletions, blocks force pushes, requires a
  pull request with one approving review, and requires the `check`,
  `cross-build`, and `semver-label` status checks with "require branches to be
  up to date". Repository admins are on the bypass list, because a solo
  maintainer cannot approve their own pull request.

### On merge queues

GitHub restricts merge queues to repositories owned by an organization, and this
one is owned by a personal account, so the queue rule cannot be enabled today.
Auto merge does the same job for a single-maintainer repository: it holds the
pull request until the required checks pass and then merges it.

The workflows are already written for a queue. `ci.yml` and `semver-label.yml`
both trigger on `merge_group`, and `semver-label` reports green there rather
than looking for labels a merge group does not have — a required check that
never reports on a merge group would stall the queue forever. If this repository
moves to an organization, turning on "Require merge queue" is a settings change
with no edits to any workflow: the same `gh pr merge --auto` call enqueues the
pull request instead of arming auto merge.

## When something goes wrong

**A merge produced no release.** The release job skips, rather than fails, when
the commit has no associated pull request — a direct push by an admin. Tag it by
hand, or open a pull request for the follow-up.

**The job failed on the version step.** The commit's pull request had zero or
more than one version label. That should be impossible while `semver-label` is a
required check, so it means the check was bypassed. Fix the labels and re-run
the workflow.

**The tag already exists.** The job notices and exits without publishing, so
re-running it is safe.

**Cutting a release by hand.** Push an annotated `vX.Y.Z` tag and run
`make release-build VERSION=vX.Y.Z`, then attach `bin/dist/*` to a release
created from the GitHub UI.
