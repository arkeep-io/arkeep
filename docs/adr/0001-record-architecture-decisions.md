# 0001. Record architecture decisions

- **Status:** Accepted
- **Date:** 2026-10-12
- **Deciders:** Arkeep maintainers

## Context

Arkeep's architecture decisions are spread across the documentation site,
`SECURITY.md`, `CONTRIBUTING.md`, package comments and pull request
discussions. New contributors cannot easily tell which choices are deliberate,
which are constraints they must respect, and why they were made. Reviewers end
up re-explaining the same decisions, and a decision can be reversed by
accident because nobody remembered it was one.

## Decision

We will record architecture decisions as Architecture Decision Records in
`docs/adr/`, one Markdown file per decision, following
[`template.md`](template.md) and the process described in
[`README.md`](README.md).

- ADRs are numbered sequentially and never renumbered.
- An accepted ADR is immutable, except for its status line when it is
  deprecated or superseded.
- No dedicated CLI tool is required: copying the template is enough.
- Decisions taken before this ADR are retro-documented (0002 to 0011) and
  marked Accepted, because they describe the code as it is.

## Consequences

- Structural choices become explicit and reviewable in pull requests.
- Changing an established decision requires a new ADR, which makes the change
  visible and keeps the reasoning that led to the original choice.
- ADRs add a small writing cost to structural changes. Routine changes are
  exempt.

## Alternatives considered

- **Keep decisions in the user documentation and code comments** — they are
  mixed with usage instructions and do not record context, alternatives or
  status.
- **ADR tooling (adr-tools, log4brains)** — adds a dependency for a task that a
  copied template handles just as well.

## References

- Michael Nygard, *Documenting Architecture Decisions* (2011).
