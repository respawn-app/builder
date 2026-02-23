package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	sessionFile = "session.json"
	eventsFile  = "events.jsonl"
)

type Store struct {
	mu         sync.Mutex
	sessionDir string
	sessionFP  string
	eventsFP   string
	meta       Meta
	persisted  bool
}

func Create(workspaceContainerDir, workspaceContainerName, workspaceRoot string) (*Store, error) {
	s, err := NewLazy(workspaceContainerDir, workspaceContainerName, workspaceRoot)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensurePersistedLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func NewLazy(workspaceContainerDir, workspaceContainerName, workspaceRoot string) (*Store, error) {
	sid := uuid.NewString()
	sessionDir := filepath.Join(workspaceContainerDir, sid)
	now := time.Now().UTC()
	return &Store{
		sessionDir: sessionDir,
		sessionFP:  filepath.Join(sessionDir, sessionFile),
		eventsFP:   filepath.Join(sessionDir, eventsFile),
		meta: Meta{
			SessionID:          sid,
			WorkspaceRoot:      workspaceRoot,
			WorkspaceContainer: workspaceContainerName,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		persisted: false,
	}, nil
}

func Open(sessionDir string) (*Store, error) {
	s := &Store{
		sessionDir: sessionDir,
		sessionFP:  filepath.Join(sessionDir, sessionFile),
		eventsFP:   filepath.Join(sessionDir, eventsFile),
		persisted:  true,
	}
	if err := s.loadMetaLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func ListSessions(workspaceContainerDir string) ([]Summary, error) {
	entries, err := os.ReadDir(workspaceContainerDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read workspace container: %w", err)
	}

	out := make([]Summary, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sessionID := e.Name()
		sessionPath := filepath.Join(workspaceContainerDir, sessionID)
		metaPath := filepath.Join(sessionPath, sessionFile)
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var m Meta
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		out = append(out, Summary{
			SessionID: m.SessionID,
			Name:      strings.TrimSpace(m.Name),
			UpdatedAt: m.UpdatedAt,
			Path:      sessionPath,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *Store) Dir() string {
	return s.sessionDir
}

func (s *Store) Meta() Meta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta
}

func (s *Store) MarkInFlight(inFlight bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.meta.InFlightStep = inFlight
	s.meta.UpdatedAt = time.Now().UTC()
	return s.persistMetaLocked()
}

func (s *Store) SetName(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.meta.Name = strings.TrimSpace(name)
	s.meta.UpdatedAt = time.Now().UTC()
	return s.persistMetaLocked()
}

func (s *Store) SetParentSessionID(parentSessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.meta.ParentSessionID = strings.TrimSpace(parentSessionID)
	s.meta.UpdatedAt = time.Now().UTC()
	return s.persistMetaLocked()
}

func (s *Store) MarkAgentsInjected() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.meta.AgentsInjected = true
	s.meta.UpdatedAt = time.Now().UTC()
	return s.persistMetaLocked()
}

func (s *Store) MarkModelDispatchLocked(contract LockedContract) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.meta.ModelRequestCount++
	if s.meta.Locked == nil {
		contract.LockedAt = time.Now().UTC()
		s.meta.Locked = &contract
	}
	s.meta.UpdatedAt = time.Now().UTC()
	return s.persistMetaLocked()
}

func (s *Store) AppendEvent(stepID, kind string, payload any) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	body, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("marshal event payload: %w", err)
	}

	evt := Event{
		Seq:       s.meta.LastSequence + 1,
		Timestamp: time.Now().UTC(),
		Kind:      kind,
		StepID:    stepID,
		Payload:   body,
	}

	if err := s.appendEventsAtomicLocked([]Event{evt}); err != nil {
		return Event{}, err
	}
	return evt, nil
}

func (s *Store) AppendTurnAtomic(stepID string, events []EventInput) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(events) == 0 {
		return nil, nil
	}
	built := make([]Event, 0, len(events))
	seq := s.meta.LastSequence
	now := time.Now().UTC()
	for _, in := range events {
		body, err := json.Marshal(in.Payload)
		if err != nil {
			return nil, fmt.Errorf("marshal event payload: %w", err)
		}
		seq++
		built = append(built, Event{
			Seq:       seq,
			Timestamp: now,
			Kind:      in.Kind,
			StepID:    stepID,
			Payload:   body,
		})
	}

	if err := s.appendEventsAtomicLocked(built); err != nil {
		return nil, err
	}
	return built, nil
}

type ReplayEvent struct {
	StepID  string
	Kind    string
	Payload json.RawMessage
}

func (s *Store) AppendReplayEvents(events []ReplayEvent) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(events) == 0 {
		return nil, nil
	}
	built := make([]Event, 0, len(events))
	seq := s.meta.LastSequence
	now := time.Now().UTC()
	for _, in := range events {
		seq++
		payload := append(json.RawMessage(nil), in.Payload...)
		built = append(built, Event{
			Seq:       seq,
			Timestamp: now,
			Kind:      in.Kind,
			StepID:    strings.TrimSpace(in.StepID),
			Payload:   payload,
		})
	}

	if err := s.appendEventsAtomicLocked(built); err != nil {
		return nil, err
	}
	return built, nil
}

type EventInput struct {
	Kind    string
	Payload any
}

func (s *Store) ReadEvents() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.persisted {
		return nil, nil
	}

	fp, err := os.Open(s.eventsFP)
	if err != nil {
		return nil, fmt.Errorf("open events file: %w", err)
	}
	defer fp.Close()

	reader := bufio.NewReader(fp)
	out := []Event{}
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return nil, fmt.Errorf("read events line: %w", readErr)
			}
			continue
		}
		var evt Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			return nil, fmt.Errorf("parse event line: %w", err)
		}
		out = append(out, evt)
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read events line: %w", readErr)
		}
	}
	return out, nil
}

func (s *Store) loadMetaLocked() error {
	data, err := os.ReadFile(s.sessionFP)
	if err != nil {
		return fmt.Errorf("read session meta: %w", err)
	}
	var m Meta
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parse session meta: %w", err)
	}
	s.meta = m
	return nil
}

func (s *Store) persistMetaLocked() error {
	if err := s.ensurePersistedLocked(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session meta: %w", err)
	}
	tmp := s.sessionFP + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write session meta tmp: %w", err)
	}
	if err := os.Rename(tmp, s.sessionFP); err != nil {
		return fmt.Errorf("replace session meta: %w", err)
	}
	return nil
}

func (s *Store) appendEventsAtomicLocked(events []Event) error {
	if err := s.ensurePersistedLocked(); err != nil {
		return err
	}
	existing, err := os.ReadFile(s.eventsFP)
	if err != nil {
		return fmt.Errorf("read events file: %w", err)
	}

	buf := bytes.NewBuffer(nil)
	if len(existing) > 0 {
		buf.Write(existing)
		if existing[len(existing)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}

	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal event line: %w", err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
		s.meta.LastSequence = e.Seq
	}
	s.meta.UpdatedAt = time.Now().UTC()

	tmp := s.eventsFP + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write events tmp: %w", err)
	}
	if err := os.Rename(tmp, s.eventsFP); err != nil {
		return fmt.Errorf("replace events: %w", err)
	}
	if err := s.persistMetaLocked(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ensurePersistedLocked() error {
	if s.persisted {
		return nil
	}
	if err := os.MkdirAll(s.sessionDir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	if err := os.WriteFile(s.eventsFP, nil, 0o644); err != nil {
		return fmt.Errorf("initialize events file: %w", err)
	}
	s.persisted = true
	return nil
}
