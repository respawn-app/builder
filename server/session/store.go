package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	sessionFile     = "session.json"
	eventsFile      = "events.jsonl"
	sessionsDirName = "sessions"
)

type Store struct {
	mu                    sync.Mutex
	sessionDir            string
	sessionFP             string
	eventsFP              string
	meta                  Meta
	conversationFreshness ConversationFreshness
	persisted             bool
	options               storeOptions
	eventsFileSizeBytes   int64
	pendingFsyncWrites    int
	writesSinceCompaction int
}

func Create(workspaceContainerDir, workspaceContainerName, workspaceRoot string, options ...StoreOption) (*Store, error) {
	s, err := NewLazy(workspaceContainerDir, workspaceContainerName, workspaceRoot, options...)
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

func NewLazy(workspaceContainerDir, workspaceContainerName, workspaceRoot string, options ...StoreOption) (*Store, error) {
	sid := uuid.NewString()
	sessionDir := filepath.Join(workspaceContainerDir, sid)
	now := time.Now().UTC()
	storeOpts := normalizeStoreOptions(options...)
	return &Store{
		sessionDir: sessionDir,
		sessionFP:  filepath.Join(sessionDir, sessionFile),
		eventsFP:   filepath.Join(sessionDir, eventsFile),
		options:    storeOpts,
		meta: Meta{
			SessionID:          sid,
			WorkspaceRoot:      workspaceRoot,
			WorkspaceContainer: workspaceContainerName,
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		conversationFreshness: ConversationFreshnessFresh,
		persisted:             false,
	}, nil
}

func Open(sessionDir string, options ...StoreOption) (*Store, error) {
	storeOpts := normalizeStoreOptions(options...)
	s := &Store{
		sessionDir: sessionDir,
		sessionFP:  filepath.Join(sessionDir, sessionFile),
		eventsFP:   filepath.Join(sessionDir, eventsFile),
		persisted:  true,
		options:    storeOpts,
	}
	if err := s.loadMetaLocked(); err != nil {
		return nil, err
	}
	if err := s.bootstrapEventLogStateLocked(); err != nil {
		return nil, err
	}
	return s, nil
}

func OpenByID(persistenceRoot, sessionID string, options ...StoreOption) (*Store, error) {
	sessionDir, err := FindSessionDir(persistenceRoot, sessionID)
	if err != nil {
		return nil, err
	}
	return Open(sessionDir, options...)
}

func FindSessionDir(persistenceRoot, sessionID string) (string, error) {
	root := strings.TrimSpace(persistenceRoot)
	id := strings.TrimSpace(sessionID)
	if root == "" {
		return "", errors.New("persistence root is required")
	}
	if id == "" {
		return "", errors.New("session id is required")
	}

	searchRoot := filepath.Join(root, sessionsDirName)
	if direct := filepath.Join(searchRoot, id); hasSessionMeta(direct) {
		return direct, nil
	}
	entries, err := os.ReadDir(searchRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("session %q not found", id)
		}
		return "", fmt.Errorf("read session root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(searchRoot, entry.Name(), id)
		if hasSessionMeta(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("session %q not found", id)
}

func hasSessionMeta(sessionDir string) bool {
	if strings.TrimSpace(sessionDir) == "" {
		return false
	}
	err := ensureRegularSessionFile(filepath.Join(sessionDir, sessionFile), "session meta")
	return err == nil
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
		data, err := readRegularSessionFile(filepath.Join(sessionPath, sessionFile), "session meta")
		if err != nil {
			continue
		}
		var m Meta
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		out = append(out, Summary{
			SessionID:          m.SessionID,
			Name:               strings.TrimSpace(m.Name),
			FirstPromptPreview: strings.TrimSpace(m.FirstPromptPreview),
			UpdatedAt:          m.UpdatedAt,
			Path:               sessionPath,
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

func (s *Store) ConversationFreshness() ConversationFreshness {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conversationFreshness
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

func (s *Store) SetInputDraft(inputDraft string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.meta.InputDraft == inputDraft && (!s.persisted || hasSessionMeta(s.sessionDir)) {
		return nil
	}
	s.meta.InputDraft = inputDraft
	s.meta.UpdatedAt = time.Now().UTC()
	if !s.persisted && inputDraft == "" {
		return nil
	}
	return s.persistMetaLocked()
}

func (s *Store) SetContinuationContext(ctx ContinuationContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.meta.Continuation = normalizeContinuationContext(ctx)
	s.meta.UpdatedAt = time.Now().UTC()
	if !s.persisted {
		return nil
	}
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
	s.captureFirstPromptPreviewLocked([]Event{evt})
	s.advanceConversationFreshnessLocked([]Event{evt})

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
	s.captureFirstPromptPreviewLocked(built)
	s.advanceConversationFreshnessLocked(built)

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
	s.captureFirstPromptPreviewLocked(built)
	s.advanceConversationFreshnessLocked(built)

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

	parsed, err := parseEventsFromReader(bufio.NewReader(fp))
	if err != nil {
		return nil, err
	}
	s.eventsFileSizeBytes = parsed.totalBytes
	return parsed.events, nil
}

func (s *Store) WalkEvents(visit func(Event) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.persisted {
		return nil
	}
	parsed, err := walkEventsFile(s.eventsFP, visit)
	if err != nil {
		return err
	}
	s.eventsFileSizeBytes = parsed.totalBytes
	return nil
}

func (s *Store) loadMetaLocked() error {
	m, err := readMetaFile(s.sessionFP)
	if err != nil {
		return err
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
	if err := s.compactEventsIfNeededLocked(); err != nil {
		return err
	}

	if _, err := s.appendEventsLogLocked(events); err != nil {
		return err
	}
	for _, e := range events {
		s.meta.LastSequence = e.Seq
	}
	s.meta.UpdatedAt = time.Now().UTC()
	s.writesSinceCompaction++
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
	s.eventsFileSizeBytes = 0
	s.pendingFsyncWrites = 0
	s.writesSinceCompaction = 0
	s.persisted = true
	return nil
}

func normalizeContinuationContext(ctx ContinuationContext) *ContinuationContext {
	openAIBaseURL := strings.TrimSpace(ctx.OpenAIBaseURL)
	if openAIBaseURL == "" {
		return nil
	}
	return &ContinuationContext{OpenAIBaseURL: openAIBaseURL}
}

func (s *Store) captureFirstPromptPreviewLocked(events []Event) {
	if strings.TrimSpace(s.meta.FirstPromptPreview) != "" {
		return
	}
	for _, evt := range events {
		if preview, ok := firstPromptPreviewFromEvent(evt.Kind, evt.Payload); ok {
			s.meta.FirstPromptPreview = preview
			return
		}
	}
}

func (s *Store) advanceConversationFreshnessLocked(events []Event) {
	if s.conversationFreshness == ConversationFreshnessEstablished {
		return
	}
	for _, evt := range events {
		if hasVisibleUserMessageEvent(evt.Kind, evt.Payload) {
			s.conversationFreshness = ConversationFreshnessEstablished
			return
		}
	}
}
