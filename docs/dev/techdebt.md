# Tech Debt

## Transcript Entry Canonicalization

- Unify live and resume handling for local transcript entries behind a single canonicalization layer instead of letting fresh runtime entries and replayed `local_entry` events drift.
- Introduce one normalization function for transcript-local entries, especially reviewer entries such as `reviewer_suggestions` and `reviewer_status`.
- Run that normalization on both live append/persist and restore replay so rendering semantics come from one contract.
- Keep legacy resume compatibility in that same path rather than in ad hoc restore-only fixes.
- Consider versioning persisted local-entry payloads if reviewer/transcript shapes continue to evolve.
