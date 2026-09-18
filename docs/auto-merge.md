# Auto-merge safety gate

The repository is prepared for auto-merge through two workflows:

- `CI` verifies formatting, module-file consistency, static analysis, race-safe tests, a coverage floor, backend and frontend builds, repository-wide linting, dependency changes, the production container build, and the container vulnerability scan.
- `CodeQL` performs extended security analysis for Go and TypeScript/JavaScript on pull requests, `main`, merge-queue commits, and a weekly schedule.

All third-party GitHub Actions are pinned to immutable commit SHAs. Renovate keeps those pins current.

## Required GitHub settings

Create a branch ruleset for `main` with the following settings:

1. Require a pull request before merging.
2. Require branches to be up to date before merging, or enable the merge queue.
3. Require these status checks:
   - `Merge gate`
   - `Analyze (go)`
   - `Analyze (javascript-typescript)`
4. Require conversation resolution.
5. Block force pushes and branch deletion.
6. Enable repository auto-merge.

`Merge gate` is the stable aggregate check. It cannot pass unless every applicable CI job succeeds. Dependency review is intentionally skipped outside pull requests, while all other checks run for pull requests, `main`, and merge-queue commits.

## Renovate policy

Renovate may auto-merge patch, pin, and digest updates after three days and minor updates after seven days. All required checks still have to pass. Major updates are never auto-merged.

## Local verification

Run the same core checks before pushing:

```bash
make check
make lint
make docker-build
```

`make lint` checks changes relative to `origin/main`. Use `make lint-all-files` when a full file-based repository scan is required.

The CI runner uses Node.js 24. Local frontend builds require Node.js 22 or newer; `make build-web` falls back to the pinned Node.js container when necessary.
