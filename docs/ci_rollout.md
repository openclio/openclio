# CI & Rollout Plan

This document describes the CI pipeline and a safe rollout plan for features.

## Goals
- Run tests and basic linting on every PR.
- Produce coverage artifacts for analysis.
- Enforce formatting and vet checks.
- Provide a release workflow for staged rollout.

## GitHub Actions
- `CI` workflow (this repo): runs on push & PR to `main`/`master`.
  - Steps: checkout, setup go, fmt check, go vet, go test, optional golangci-lint, coverage artifact upload.

## Release & Rollout
1. Feature branch → PR to `main`.
2. CI must pass and reviewers approve.
3. Merge to `main` triggers:
   - Build artifacts (binaries) for Linux/macOS via Go cross-compile.
   - Create draft GitHub release with changelog snippet.
4. Staged rollout:
   - Tag `vX.Y.Z-rc` and deploy to a "staging" host / container registry.
   - Smoke tests / e2e run against staging.
   - If OK, promote tag to final `vX.Y.Z` and publish artifacts.

## Feature Flags
- Use simple config-driven feature flags in `~/.openclio/config.yaml` under a `features:` map.
- For complex toggles, integrate a remote flag provider (opt-in).

## Monitoring & Observability
- Collect runtime metrics (tool call counts, redaction counts) — currently implemented in-memory.
- Forward metrics to a monitoring backend (Prometheus pushgateway or file export) in a future PR.

## CI Maintenance
- Periodically update Go versions in CI.
- Add golangci-lint in CI once it is adopted (caching container).

