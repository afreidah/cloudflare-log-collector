# Contributing

Contributions are welcome. A few notes to keep things smooth.

## Pull requests

- This repository uses [release-please](https://github.com/googleapis/release-please),
  which derives the next version and the changelog from commit messages, so
  please follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `chore:`, `ci:`, `docs:`, ...).
- Before opening a PR, run the local checks: `make lint`, `make vet`,
  `make test`, and `make govulncheck`.
- CI runs lint, tests, and `govulncheck` on every PR.

## Forking

Most account-specific values are derived automatically, so a fork needs little
or no editing:

- **Container registry** — defaults to `ghcr.io/<your-account>` (derived from the
  `origin` remote). Override with `DOCKER_REGISTRY=<registry> make push` for a
  private registry.
- **GitHub release target** — GoReleaser auto-detects the owner/repo from the
  remote; nothing to change.

A few values are tied to an account and only matter if you run your **own**
release/analysis pipeline (not needed to contribute back upstream):

| What | Where | When you need to change it |
|------|-------|----------------------------|
| `RELEASE_PAT` secret | repo/org secrets | Required for release-please to open release PRs that trigger CI. |
| `sonar.organization` / `sonar.projectKey` | `sonar-project.properties` | Only if you wire the fork up to your own SonarCloud project. |
| Module path | `go.mod` (`module github.com/...`) | Only if you hard-fork and republish under your own import path. |

Contributing a PR back upstream requires none of the above — your changes are
built and analyzed against the upstream repository's own CI, secrets, and
SonarCloud project.
