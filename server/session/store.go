package session

import (
	"bufio"
	"context"
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

var ErrSessionNotFound = errors.New("session not found")

type Store struct {
	mu                    sync.Mutex
	sessionDir            string
	sessionFP             string
	eventsFP              string
	meta                  Meta
	conversationFreshness ConversationFreshness
	persisted             bool
	metadataVersion       uint64
	persistedMetaVersion  uint64
	options               storeOptions
	eventsFileSizeBytes   int64
	pendingFsyncWrites    int
	writesSinceCompaction int
}

type persistenceObservation struct {
	snapshot *PersistedStoreSnapshot
	version  uint64
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
	storeOpts := normalizeStoreOptions(options...)
	return newLazyWithStoreOptions(workspaceContainerDir, workspaceContainerName, workspaceRoot, storeOpts)
}

func newLazyWithStoreOptions(workspaceContainerDir, workspaceContainerName, workspaceRoot string, storeOpts storeOptions) (*Store, error) {
	sid := uuid.NewString()
	sessionDir := filepath.Join(workspaceContainerDir, sid)
	now := time.Now().UTC()
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
	return openPersistedSession(sessionDir, nil, storeOpts)
}

func OpenByID(persistenceRoot, sessionID string, options ...StoreOption) (*Store, error) {
	storeOpts := normalizeStoreOptions(options...)
	record, err := resolvePersistedSessionRecord(persistenceRoot, sessionID, storeOpts)
	if err != nil {
		return nil, err
	}
	return openPersistedSession(record.SessionDir, record.Meta, storeOpts)
}

func openPersistedSession(sessionDir string, resolvedMeta *Meta, storeOpts storeOptions) (*Store, error) {
	s := &Store{
		sessionDir: sessionDir,
		sessionFP:  filepath.Join(sessionDir, sessionFile),
		eventsFP:   filepath.Join(sessionDir, eventsFile),
		persisted:  true,
		options:    storeOpts,
	}
	if resolvedMeta != nil {
		s.meta = *resolvedMeta
	} else if err := s.loadMetaLocked(); err != nil {
		return nil, err
	}
	s.metadataVersion = 1
	s.persistedMetaVersion = 1
	if err := s.bootstrapEventLogStateLocked(); err != nil {
		return nil, err
	}
	if err := s.observePersistence(&persistenceObservation{snapshot: s.persistenceSnapshotLocked(), version: s.metadataVersion}); err != nil {
		return nil, err
	}
	return s, nil
}

func resolvePersistedSessionRecord(persistenceRoot, sessionID string, storeOpts storeOptions) (PersistedSessionRecord, error) {
	root := strings.TrimSpace(persistenceRoot)
	id := strings.TrimSpace(sessionID)
	if root == "" {
		return PersistedSessionRecord{}, errors.New("persistence root is required")
	}
	if id == "" {
		return PersistedSessionRecord{}, errors.New("session id is required")
	}
	if sessionDir, err := FindSessionDir(root, id); err == nil {
		return PersistedSessionRecord{SessionDir: sessionDir}, nil
	} else if storeOpts.resolver == nil || !errors.Is(err, ErrSessionNotFound) {
		return PersistedSessionRecord{}, err
	}
	record, err := storeOpts.resolver.ResolvePersistedSession(context.Background(), id)
	if err != nil {
		return PersistedSessionRecord{}, err
	}
	if strings.TrimSpace(record.SessionDir) == "" {
		return PersistedSessionRecord{}, fmt.Errorf("resolver returned invalid persisted session record for %q: missing session dir", id)
	}
	if !filepath.IsAbs(record.SessionDir) || filepath.Clean(record.SessionDir) != record.SessionDir {
		return PersistedSessionRecord{}, fmt.Errorf("resolver returned invalid persisted session record for %q: session dir must be an absolute clean path", id)
	}
	if record.Meta == nil {
		return PersistedSessionRecord{}, fmt.Errorf("resolver returned invalid persisted session record for %q: missing metadata", id)
	}
	return record, nil
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
			return "", fmt.Errorf("%w: %q", ErrSessionNotFound, id)
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
	return "", fmt.Errorf("%w: %q", ErrSessionNotFound, id)
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

func (s *Store) mutateAndPersist(mutator func() error) error {
	s.mu.Lock()
	if err := mutator(); err != nil {
		s.mu.Unlock()
		return err
	}
	snapshot, err := s.persistMetaLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.observePersistence(snapshot)
}

func (s *Store) EnsureDurable() error {
	return s.mutateAndPersist(func() error { return nil })
}

func (s *Store) MarkInFlight(inFlight bool) error {
	return s.mutateAndPersist(func() error {
		s.meta.InFlightStep = inFlight
		s.meta.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (s *Store) SetName(name string) error {
	return s.mutateAndPersist(func() error {
		s.meta.Name = strings.TrimSpace(name)
		s.meta.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (s *Store) SetParentSessionID(parentSessionID string) error {
	return s.mutateAndPersist(func() error {
		s.meta.ParentSessionID = strings.TrimSpace(parentSessionID)
		s.meta.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (s *Store) SetInputDraft(inputDraft string) error {
	s.mu.Lock()

	if s.meta.InputDraft == inputDraft && (!s.persisted || s.hasDurableMetadataLocked()) {
		s.mu.Unlock()
		return nil
	}
	s.meta.InputDraft = inputDraft
	s.meta.UpdatedAt = time.Now().UTC()
	if !s.persisted && inputDraft == "" {
		s.mu.Unlock()
		return nil
	}
	snapshot, err := s.persistMetaLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.observePersistence(snapshot)
}

func (s *Store) SetCompactionSoonReminderIssued(issued bool) error {
	s.mu.Lock()
	if s.meta.CompactionSoonReminderIssued == issued && (!s.persisted || s.hasDurableMetadataLocked()) {
		s.mu.Unlock()
		return nil
	}
	s.meta.CompactionSoonReminderIssued = issued
	s.meta.UpdatedAt = time.Now().UTC()
	snapshot, err := s.persistMetaLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.observePersistence(snapshot)
}

func (s *Store) SetUsageState(state *UsageState) error {
	s.mu.Lock()

	normalized := normalizeUsageState(state)
	if usageStatesEqual(s.meta.UsageState, normalized) && (!s.persisted || s.hasDurableMetadataLocked()) {
		s.mu.Unlock()
		return nil
	}
	s.meta.UsageState = normalized
	s.meta.UpdatedAt = time.Now().UTC()
	snapshot, err := s.persistMetaLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.observePersistence(snapshot)
}

func (s *Store) SetContinuationContext(ctx ContinuationContext) error {
	s.mu.Lock()

	s.meta.Continuation = normalizeContinuationContext(ctx)
	s.meta.UpdatedAt = time.Now().UTC()
	if !s.persisted {
		s.mu.Unlock()
		return nil
	}
	snapshot, err := s.persistMetaLocked()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.observePersistence(snapshot)
}

func (s *Store) MarkAgentsInjected() error {
	return s.mutateAndPersist(func() error {
		s.meta.AgentsInjected = true
		s.meta.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (s *Store) MarkModelDispatchLocked(contract LockedContract) error {
	return s.mutateAndPersist(func() error {
		s.meta.ModelRequestCount++
		if s.meta.Locked == nil {
			contract.LockedAt = time.Now().UTC()
			s.meta.Locked = &contract
		}
		s.meta.UpdatedAt = time.Now().UTC()
		return nil
	})
}

func (s *Store) AppendEvent(stepID, kind string, payload any) (Event, error) {
	s.mu.Lock()

	body, err := json.Marshal(payload)
	if err != nil {
		s.mu.Unlock()
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

	observation, err := s.appendEventsAtomicLocked([]Event{evt})
	if err != nil {
		s.mu.Unlock()
		return Event{}, err
	}
	s.mu.Unlock()
	if err := s.observePersistence(observation); err != nil {
		return Event{}, err
	}
	return evt, nil
}

func (s *Store) AppendTurnAtomic(stepID string, events []EventInput) ([]Event, error) {
	s.mu.Lock()

	if len(events) == 0 {
		s.mu.Unlock()
		return nil, nil
	}
	built := make([]Event, 0, len(events))
	seq := s.meta.LastSequence
	now := time.Now().UTC()
	for _, in := range events {
		body, err := json.Marshal(in.Payload)
		if err != nil {
			s.mu.Unlock()
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

	observation, err := s.appendEventsAtomicLocked(built)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	if err := s.observePersistence(observation); err != nil {
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

	if len(events) == 0 {
		s.mu.Unlock()
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

	observation, err := s.appendEventsAtomicLocked(built)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()
	if err := s.observePersistence(observation); err != nil {
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
	if err == nil {
		s.meta = m
		return nil
	}
	if s.options.resolver == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}
	record, resolveErr := s.options.resolver.ResolvePersistedSession(context.Background(), filepath.Base(s.sessionDir))
	if resolveErr != nil {
		return fmt.Errorf("%w (resolver fallback failed: %v)", err, resolveErr)
	}
	if record.Meta == nil {
		return fmt.Errorf("%w (resolver fallback returned nil metadata)", err)
	}
	s.meta = *record.Meta
	return nil
}

func (s *Store) persistMetaLocked() (*persistenceObservation, error) {
	if err := s.ensurePersistedLocked(); err != nil {
		return nil, err
	}
	s.metadataVersion++
	if !s.options.filelessMeta {
		data, err := json.MarshalIndent(s.meta, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal session meta: %w", err)
		}
		tmp := s.sessionFP + ".tmp"
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			return nil, fmt.Errorf("write session meta tmp: %w", err)
		}
		if err := os.Rename(tmp, s.sessionFP); err != nil {
			return nil, fmt.Errorf("replace session meta: %w", err)
		}
		s.persistedMetaVersion = s.metadataVersion
	}
	return &persistenceObservation{snapshot: s.persistenceSnapshotLocked(), version: s.metadataVersion}, nil
}

func (s *Store) hasDurableMetadataLocked() bool {
	if s == nil || !s.persisted {
		return false
	}
	if hasSessionMeta(s.sessionDir) {
		return true
	}
	if !s.options.filelessMeta {
		return false
	}
	return s.metadataVersion != 0 && s.persistedMetaVersion == s.metadataVersion
}

func (s *Store) appendEventsAtomicLocked(events []Event) (*persistenceObservation, error) {
	if err := s.ensurePersistedLocked(); err != nil {
		return nil, err
	}
	if err := s.compactEventsIfNeededLocked(); err != nil {
		return nil, err
	}

	if _, err := s.appendEventsLogLocked(events); err != nil {
		return nil, err
	}
	for _, e := range events {
		s.meta.LastSequence = e.Seq
	}
	s.meta.UpdatedAt = time.Now().UTC()
	s.writesSinceCompaction++
	snapshot, err := s.persistMetaLocked()
	if err != nil {
		return nil, err
	}
	return snapshot, nil
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

func (s *Store) persistenceSnapshotLocked() *PersistedStoreSnapshot {
	if s == nil || !s.persisted || s.options.observer == nil {
		return nil
	}
	snapshot := PersistedStoreSnapshot{
		SessionDir: s.sessionDir,
		Meta:       s.meta,
	}
	return &snapshot
}

func (s *Store) observePersistence(observation *persistenceObservation) error {
	if s == nil || observation == nil || observation.snapshot == nil || s.options.observer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.options.observerTimeout)
	defer cancel()
	if err := s.options.observer.ObservePersistedStore(ctx, *observation.snapshot); err != nil {
		return err
	}
	if s.options.filelessMeta {
		s.mu.Lock()
		if observation.version > s.persistedMetaVersion {
			s.persistedMetaVersion = observation.version
		}
		s.mu.Unlock()
	}
	return nil
}

func normalizeContinuationContext(ctx ContinuationContext) *ContinuationContext {
	openAIBaseURL := strings.TrimSpace(ctx.OpenAIBaseURL)
	if openAIBaseURL == "" {
		return nil
	}
	return &ContinuationContext{OpenAIBaseURL: openAIBaseURL}
}

func normalizeUsageState(state *UsageState) *UsageState {
	if state == nil {
		return nil
	}
	normalized := *state
	if normalized.InputTokens < 0 {
		normalized.InputTokens = 0
	}
	if normalized.OutputTokens < 0 {
		normalized.OutputTokens = 0
	}
	if normalized.WindowTokens < 0 {
		normalized.WindowTokens = 0
	}
	if normalized.CachedInputTokens < 0 {
		normalized.CachedInputTokens = 0
	}
	if normalized.CachedInputTokens > normalized.InputTokens {
		normalized.CachedInputTokens = normalized.InputTokens
	}
	if normalized.EstimatedProviderTokens < 0 {
		normalized.EstimatedProviderTokens = 0
	}
	if normalized.TotalInputTokens < 0 {
		normalized.TotalInputTokens = 0
	}
	if normalized.TotalCachedInputTokens < 0 {
		normalized.TotalCachedInputTokens = 0
	}
	if normalized.TotalCachedInputTokens > normalized.TotalInputTokens {
		normalized.TotalCachedInputTokens = normalized.TotalInputTokens
	}
	if normalized.InputTokens == 0 && normalized.OutputTokens == 0 && normalized.WindowTokens == 0 && normalized.CachedInputTokens == 0 && !normalized.HasCachedInputTokens && normalized.EstimatedProviderTokens == 0 && normalized.TotalInputTokens == 0 && normalized.TotalCachedInputTokens == 0 {
		return nil
	}
	return &normalized
}

func usageStatesEqual(left, right *UsageState) bool {
	left = normalizeUsageState(left)
	right = normalizeUsageState(right)
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
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
		s.conversationFreshness = advanceConversationFreshness(s.conversationFreshness, evt)
		if s.conversationFreshness == ConversationFreshnessEstablished {
			return
		}
	}
}
