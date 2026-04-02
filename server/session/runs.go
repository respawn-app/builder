package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	runStartedEventKind  = "run_started"
	runFinishedEventKind = "run_finished"
)

type RunStatus string

const (
	RunStatusRunning     RunStatus = "running"
	RunStatusCompleted   RunStatus = "completed"
	RunStatusInterrupted RunStatus = "interrupted"
	RunStatusFailed      RunStatus = "failed"
)

type RunRecord struct {
	RunID      string    `json:"run_id"`
	StepID     string    `json:"step_id,omitempty"`
	Status     RunStatus `json:"status"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

func (s *Store) AppendRunStarted(run RunRecord) (Event, error) {
	started := normalizeRunRecord(run)
	started.Status = RunStatusRunning
	if started.StartedAt.IsZero() {
		started.StartedAt = time.Now().UTC()
	}
	return s.AppendEvent(started.StepID, runStartedEventKind, started)
}

func (s *Store) AppendRunFinished(run RunRecord) (Event, error) {
	finished := normalizeRunRecord(run)
	if !isTerminalRunStatus(finished.Status) {
		return Event{}, fmt.Errorf("finished run requires a terminal status")
	}
	if finished.FinishedAt.IsZero() {
		finished.FinishedAt = time.Now().UTC()
	}
	return s.AppendEvent(finished.StepID, runFinishedEventKind, finished)
}

func (s *Store) ReadRuns() ([]RunRecord, error) {
	events, err := s.ReadEvents()
	if err != nil {
		return nil, err
	}
	return runsFromEvents(events), nil
}

func (s *Store) LatestRun() (*RunRecord, error) {
	runs, err := s.ReadRuns()
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	latest := runs[len(runs)-1]
	return &latest, nil
}

func runsFromEvents(events []Event) []RunRecord {
	orderedIDs := make([]string, 0)
	byID := make(map[string]RunRecord)
	for _, evt := range events {
		kind := strings.TrimSpace(evt.Kind)
		if kind != runStartedEventKind && kind != runFinishedEventKind {
			continue
		}
		if len(evt.Payload) == 0 {
			continue
		}
		var run RunRecord
		if err := json.Unmarshal(evt.Payload, &run); err != nil {
			continue
		}
		run = normalizeRunRecord(run)
		if run.RunID == "" {
			continue
		}
		if run.StepID == "" {
			run.StepID = strings.TrimSpace(evt.StepID)
		}
		if kind == runStartedEventKind {
			run.Status = RunStatusRunning
			if run.StartedAt.IsZero() {
				run.StartedAt = evt.Timestamp
			}
		} else if run.FinishedAt.IsZero() {
			run.FinishedAt = evt.Timestamp
		}

		existing, ok := byID[run.RunID]
		if !ok {
			orderedIDs = append(orderedIDs, run.RunID)
			byID[run.RunID] = run
			continue
		}
		byID[run.RunID] = mergeRunRecord(existing, run)
	}

	out := make([]RunRecord, 0, len(orderedIDs))
	for _, runID := range orderedIDs {
		out = append(out, byID[runID])
	}
	return out
}

func normalizeRunRecord(run RunRecord) RunRecord {
	run.RunID = strings.TrimSpace(run.RunID)
	run.StepID = strings.TrimSpace(run.StepID)
	run.Status = RunStatus(strings.TrimSpace(string(run.Status)))
	return run
}

func isTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusCompleted, RunStatusInterrupted, RunStatusFailed:
		return true
	default:
		return false
	}
}

func mergeRunRecord(existing, next RunRecord) RunRecord {
	merged := existing
	if merged.StepID == "" {
		merged.StepID = next.StepID
	}
	if merged.StartedAt.IsZero() {
		merged.StartedAt = next.StartedAt
	}
	if !next.StartedAt.IsZero() && (merged.StartedAt.IsZero() || next.StartedAt.Before(merged.StartedAt)) {
		merged.StartedAt = next.StartedAt
	}
	if !next.FinishedAt.IsZero() {
		merged.FinishedAt = next.FinishedAt
	}
	if next.Status != "" && next.Status != RunStatusRunning {
		merged.Status = next.Status
	} else if merged.Status == "" {
		merged.Status = next.Status
	}
	return merged
}
