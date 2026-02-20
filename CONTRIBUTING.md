# Contributing to Arkeep

Thank you for your interest in contributing to Arkeep.

## Before you start

- Check the [open issues](https://github.com/arkeep/arkeep/issues) to see if your
  idea or bug is already being tracked
- For significant changes, open an issue first to discuss the approach before writing code
- Enterprise features are not open to external contributions

## Development setup

See the [Development section in README.md](README.md#development) for prerequisites
and setup instructions.

## Guidelines

**Go code**
- Follow standard Go conventions — `gofmt`, idiomatic error handling, small interfaces
- Table-driven tests with the standard `testing` package — no external test frameworks
- Run `task lint` before submitting

**Vue/TypeScript code**
- Composition API with `<script setup>` only — no Options API
- Type everything — no implicit `any`
- Run `task lint` before submitting

**Security**
- Never log credentials, tokens, or sensitive data
- Never expose Restic commands or terminology outside the restic package
- If you find a security vulnerability, email security@arkeep.io instead of
  opening a public issue

**Commits**
- Use [Conventional Commits](https://www.conventionalcommits.org/) format
- Prefixes: `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`

## Pull request process

1. Fork the repository and create a branch from `main`
2. Make your changes with tests where appropriate
3. Run `task test` and `task lint` — both must pass
4. Open a pull request with a clear description of what and why

## License

By contributing you agree that your contributions will be licensed under AGPLv3.