# Contributing to openclio

Contributions are welcome — bug reports, feature requests, documentation improvements, and code.

## Development Setup

1. **Clone the repository:**
   ```bash
   git clone https://github.com/openclio/openclio.git
   cd openclio
   ```

2. **Build:**
   ```bash
   make build
   # Binary at bin/openclio
   ```

3. **Run tests:**
   ```bash
   make test
   ```

4. **Lint:**
   ```bash
   make lint
   ```

See [AGENTS.md](AGENTS.md) for a full guide to the codebase structure, conventions, and data flow.

## Pull Request Process

1. Fork the repo and create your branch from `main`.
2. If you've added code that should be tested, add tests.
3. Update relevant documentation if you change external APIs or add features.
4. Ensure the test suite passes (`make test`) and lint is clean (`make lint`).
5. Open a Pull Request with a clear description of what the change does and why.

## Adding Features

- **New LLM provider:** See `internal/agent/provider.go` for the interface, then look at any existing provider as a template.
- **New tool:** See [docs/custom-tools.md](docs/custom-tools.md) and `internal/tools/registry.go`.
- **New channel adapter:** See [docs/plugins.md](docs/plugins.md) and [docs/channels.md](docs/channels.md).

## Reporting Bugs

Open a GitHub Issue with:
- openclio version (`openclio version`)
- OS and architecture
- Steps to reproduce
- What you expected vs what happened

For security vulnerabilities, see [SECURITY.md](SECURITY.md) for the responsible disclosure process — please do not open a public issue.

## Code of Conduct

See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md). Be respectful and constructive.
