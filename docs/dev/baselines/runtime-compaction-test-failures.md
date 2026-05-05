# Runtime Compaction Test Failures

## 2026-05-05

Observed while validating goal UI/transcript changes:

- `./scripts/test.sh ./...`
- `./scripts/test.sh ./server/runtime -run 'TestRunStepLoopTriggerHandoffOmitsCallAndOutputFromFollowUpRequestAndKeepsFutureMessage|TestReopenedSessionAfterTriggerHandoffUsesRotatedRequestSessionAndOmitsLingeringCallOutput' -count=1`

Both fail with:

- `runStepLoop: auto compaction did not reduce context below threshold`

Failing tests:

- `TestRunStepLoopTriggerHandoffOmitsCallAndOutputFromFollowUpRequestAndKeepsFutureMessage`
- `TestReopenedSessionAfterTriggerHandoffUsesRotatedRequestSessionAndOmitsLingeringCallOutput`

These failures reproduce in the focused `server/runtime` test run and are not caused by the goal feedback UI/transcript changes.
