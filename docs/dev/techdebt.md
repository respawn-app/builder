# Tech Debt

## Transcript Entry Canonicalization

- Unify live and resume handling for local transcript entries behind a single canonicalization layer instead of letting fresh runtime entries and replayed `local_entry` events drift.
- Introduce one normalization function for transcript-local entries, especially reviewer entries such as `reviewer_suggestions` and `reviewer_status`.
- Run that normalization on both live append/persist and restore replay so rendering semantics come from one contract.
- Keep legacy resume compatibility in that same path rather than in ad hoc restore-only fixes.
- Consider versioning persisted local-entry payloads if reviewer/transcript shapes continue to evolve.

## TUI Style Pipeline

- Formalize the rendering stages as explicit contracts: `content render -> low-level semantic transform -> wrap -> line layout -> final decoration`.
- Define style ownership clearly:
  - formatter config owns syntax backgrounds/formatter base foreground
  - transcript rendering owns role styling, subdued shell preview styling, and diff semantics
  - layout owns prefixes, indentation, and wrapping only
- Replace effect-oriented helpers with typed style intents such as `ThemeForeground`, `Subdued`, `ShellPreview`, `SyntaxHighlighted`, `DiffAdded`, and `DiffRemoved`.
- Add a small renderer-style adapter layer so Chroma and Glamour theme adjustments are centralized instead of embedded in renderer methods.
- Reduce policy leakage into ANSI-level transforms; ANSI rewriting should be transport-oriented, not the primary place where styling decisions live.
- Add shared style inspection test utilities for semantic assertions such as foreground ownership, background absence, reset behavior, and faint/subdued output.
- Add visual snapshot coverage for representative cases in both themes:
  - ongoing shell preview
  - detail shell preview
  - markdown
  - diff/file lines
  - wrapped highlighted lines
- Document rendering/style invariants in one place, including:
  - detail shell commands are full syntax color
  - ongoing shell commands are syntax-highlighted but subdued
  - formatted text uses app foreground as its base text color
  - syntax-highlighted output must not emit backgrounds unless explicitly intended
