package shell

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	xansi "github.com/charmbracelet/x/ansi"
)

const (
	defaultExecYieldTime   = 10 * time.Second
	defaultWriteYieldTime  = 250 * time.Millisecond
	closeGracePeriod       = 1 * time.Second
	closeWaitTimeout       = 5 * time.Second
	minYieldTime           = 250 * time.Millisecond
	maxYieldTime           = 30 * time.Second
	defaultOutputTokenCap  = 10_000
	maxPendingOutputBytes  = 1 << 20
	maxRecentPreviewBytes  = 4096
	backgroundLogDirPrefix = "builder-bg-shells-"
	initialProcessID       = 1000
)

type EventType string

const (
	EventBackgrounded EventType = "backgrounded"
	EventCompleted    EventType = "completed"
	EventKilled       EventType = "killed"
)

type Event struct {
	Type             EventType
	Snapshot         Snapshot
	Preview          string
	Removed          int
	NoticeSuppressed bool
}

type Snapshot struct {
	ID             string
	OwnerSessionID string
	State          string
	Command        string
	Workdir        string
	StartedAt      time.Time
	FinishedAt     time.Time
	ExitCode       *int
	LogPath        string
	RecentOutput   string
	Running        bool
	StdinOpen      bool
	Backgrounded   bool
	KillRequested  bool
	LastUpdatedAt  time.Time
}

type ExecRequest struct {
	Command        []string
	DisplayCommand string
	OwnerSessionID string
	Workdir        string
	YieldTime      time.Duration
	MaxOutputChars int
	KeepStdinOpen  bool
}

type ExecResult struct {
	SessionID         string
	WallTime          time.Duration
	Output            string
	OutputPath        string
	ExitCode          *int
	Running           bool
	Backgrounded      bool
	OriginalChars     int
	Truncated         bool
	TruncationBytes   int
	MovedToBackground bool
}

type BackgroundNoticeSummary struct {
	DetailText  string
	OngoingText string
	LineCount   int
	Truncated   bool
	LogPath     string
}

type BackgroundOutputMode string

const (
	BackgroundOutputDefault BackgroundOutputMode = "default"
	BackgroundOutputVerbose BackgroundOutputMode = "verbose"
	BackgroundOutputConcise BackgroundOutputMode = "concise"
)

type BackgroundNoticeOptions struct {
	MaxChars          int
	SuccessOutputMode BackgroundOutputMode
}

type WriteRequest struct {
	SessionID      string
	Input          string
	YieldTime      time.Duration
	MaxOutputChars int
}

type processEntry struct {
	id             string
	ownerSessionID string
	command        string
	workdir        string
	startedAt      time.Time
	finishedAt     time.Time
	exitCode       *int
	state          string
	backgrounded   bool
	logPath        string
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	logFile        *os.File
	running        bool
	stdinOpen      bool
	lastUpdatedAt  time.Time
	recentOutput   []byte
	pendingOutput  []byte
	notify         chan struct{}
	done           chan struct{}
	killRequested  bool
	noticeConsumed bool
	mu             sync.Mutex
	interactMu     sync.Mutex
}

func (p *processEntry) signal() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *processEntry) writeOutput(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.logFile != nil {
		if _, err := p.logFile.Write(chunk); err != nil {
			return err
		}
	}
	p.pendingOutput = append(p.pendingOutput, chunk...)
	if len(p.pendingOutput) > maxPendingOutputBytes {
		p.pendingOutput = append([]byte(nil), p.pendingOutput[len(p.pendingOutput)-maxPendingOutputBytes:]...)
	}
	p.recentOutput = append(p.recentOutput, chunk...)
	if len(p.recentOutput) > maxRecentPreviewBytes {
		p.recentOutput = append([]byte(nil), p.recentOutput[len(p.recentOutput)-maxRecentPreviewBytes:]...)
	}
	p.lastUpdatedAt = time.Now().UTC()
	p.signal()
	return nil
}

func (p *processEntry) setExited(exitCode int, state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = false
	p.finishedAt = time.Now().UTC()
	p.lastUpdatedAt = p.finishedAt
	p.exitCode = &exitCode
	p.state = state
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	if p.logFile != nil {
		_ = p.logFile.Sync()
		_ = p.logFile.Close()
		p.logFile = nil
	}
	p.signal()
}

func (p *processEntry) isBackgrounded() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.backgrounded
}

func (p *processEntry) closeOnExit(exitCode int, state string) Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	finishedAt := time.Now().UTC()
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	if p.logFile != nil {
		_ = p.logFile.Sync()
		_ = p.logFile.Close()
		p.logFile = nil
	}
	p.finishedAt = finishedAt
	p.lastUpdatedAt = finishedAt
	p.exitCode = &exitCode
	p.state = state
	p.running = false
	p.signal()
	return Snapshot{
		ID:             p.id,
		OwnerSessionID: p.ownerSessionID,
		State:          p.state,
		Command:        p.command,
		Workdir:        p.workdir,
		StartedAt:      p.startedAt,
		FinishedAt:     p.finishedAt,
		ExitCode:       cloneIntPtr(p.exitCode),
		LogPath:        p.logPath,
		RecentOutput:   sanitizeOutput(string(p.recentOutput)),
		Running:        false,
		StdinOpen:      p.stdinOpen,
		Backgrounded:   p.backgrounded,
		KillRequested:  p.killRequested,
		LastUpdatedAt:  p.lastUpdatedAt,
	}
}

func (p *processEntry) finalizeClosedExit() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running = false
	p.signal()
}

func (p *processEntry) markCompletionNoticeConsumed() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.backgrounded || p.exitCode == nil {
		return
	}
	p.noticeConsumed = true
}

func (p *processEntry) completionNoticeConsumed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.noticeConsumed
}

func (p *processEntry) snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Snapshot{
		ID:             p.id,
		OwnerSessionID: p.ownerSessionID,
		State:          p.state,
		Command:        p.command,
		Workdir:        p.workdir,
		StartedAt:      p.startedAt,
		FinishedAt:     p.finishedAt,
		ExitCode:       cloneIntPtr(p.exitCode),
		LogPath:        p.logPath,
		RecentOutput:   sanitizeOutput(string(p.recentOutput)),
		Running:        p.running,
		StdinOpen:      p.stdinOpen,
		Backgrounded:   p.backgrounded,
		KillRequested:  p.killRequested,
		LastUpdatedAt:  p.lastUpdatedAt,
	}
}

func (p *processEntry) drainPending() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pendingOutput) == 0 {
		return nil
	}
	out := append([]byte(nil), p.pendingOutput...)
	p.pendingOutput = nil
	return out
}

func (p *processEntry) isRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *processEntry) transitionToBackground() (Snapshot, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return Snapshot{
			ID:             p.id,
			OwnerSessionID: p.ownerSessionID,
			State:          p.state,
			Command:        p.command,
			Workdir:        p.workdir,
			StartedAt:      p.startedAt,
			FinishedAt:     p.finishedAt,
			ExitCode:       cloneIntPtr(p.exitCode),
			LogPath:        p.logPath,
			RecentOutput:   sanitizeOutput(string(p.recentOutput)),
			Running:        p.running,
			StdinOpen:      p.stdinOpen,
			Backgrounded:   p.backgrounded,
			KillRequested:  p.killRequested,
			LastUpdatedAt:  p.lastUpdatedAt,
		}, false
	}
	p.backgrounded = true
	p.state = "running"
	return Snapshot{
		ID:             p.id,
		OwnerSessionID: p.ownerSessionID,
		State:          p.state,
		Command:        p.command,
		Workdir:        p.workdir,
		StartedAt:      p.startedAt,
		FinishedAt:     p.finishedAt,
		ExitCode:       cloneIntPtr(p.exitCode),
		LogPath:        p.logPath,
		RecentOutput:   sanitizeOutput(string(p.recentOutput)),
		Running:        p.running,
		StdinOpen:      p.stdinOpen,
		Backgrounded:   p.backgrounded,
		KillRequested:  p.killRequested,
		LastUpdatedAt:  p.lastUpdatedAt,
	}, true
}

type outputWriter struct {
	entry *processEntry
}

func (w *outputWriter) Write(p []byte) (int, error) {
	if err := w.entry.writeOutput(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

type Manager struct {
	mu      sync.Mutex
	nextID  int
	entries map[string]*processEntry
	tempDir string
	onEvent func(Event)
	closed  bool
}

func NewManager() (*Manager, error) {
	tempDir, err := os.MkdirTemp("", backgroundLogDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("create background shell temp dir: %w", err)
	}
	return &Manager{nextID: initialProcessID, entries: make(map[string]*processEntry), tempDir: tempDir}, nil
}

func (m *Manager) TempDir() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tempDir
}

func (m *Manager) SetEventHandler(handler func(Event)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = handler
}

func (m *Manager) Start(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if len(req.Command) == 0 {
		return ExecResult{}, errors.New("command is required")
	}
	workdir := strings.TrimSpace(req.Workdir)
	if workdir == "" {
		return ExecResult{}, errors.New("workdir is required")
	}
	yieldTime := clampYieldTime(req.YieldTime, defaultExecYieldTime)
	maxOutputChars := normalizeOutputChars(req.MaxOutputChars)

	id, logPath, err := m.allocateProcessSlot()
	if err != nil {
		return ExecResult{}, err
	}
	cmd := exec.CommandContext(context.Background(), req.Command[0], req.Command[1:]...)
	cmd.Dir = workdir
	cmd.Env = enrichEnv(os.Environ())
	prepareManagedExec(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ExecResult{}, fmt.Errorf("open stdin pipe: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return ExecResult{}, fmt.Errorf("open log file: %w", err)
	}
	entry := &processEntry{
		id:             id,
		ownerSessionID: strings.TrimSpace(req.OwnerSessionID),
		command:        strings.TrimSpace(req.DisplayCommand),
		workdir:        workdir,
		startedAt:      time.Now().UTC(),
		lastUpdatedAt:  time.Now().UTC(),
		state:          "starting",
		logPath:        logPath,
		cmd:            cmd,
		stdin:          stdin,
		logFile:        logFile,
		running:        true,
		stdinOpen:      req.KeepStdinOpen,
		notify:         make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
	if entry.command == "" {
		entry.command = strings.Join(req.Command, " ")
	}
	writer := &outputWriter{entry: entry}
	cmd.Stdout = writer
	cmd.Stderr = writer
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		m.releaseEntry(id)
		return ExecResult{}, fmt.Errorf("start process: %w", err)
	}
	if !req.KeepStdinOpen {
		if err := stdin.Close(); err != nil {
			_ = killManagedProcess(cmd.Process)
			_, _ = m.collectUntil(context.Background(), entry, time.Now().Add(closeGracePeriod))
			_ = logFile.Close()
			m.releaseEntry(id)
			return ExecResult{}, fmt.Errorf("close stdin: %w", err)
		}
		entry.stdin = nil
	}
	entry.state = "running"

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		killManagedProcess(cmd.Process)
		return ExecResult{}, errors.New("background shell manager is closed")
	}
	m.entries[id] = entry
	m.mu.Unlock()

	go m.waitForExit(entry)

	start := time.Now()
	output, err := m.collectUntil(ctx, entry, time.Now().Add(yieldTime))
	if err != nil {
		killManagedProcess(cmd.Process)
		return ExecResult{}, err
	}
	wallTime := time.Since(start)
	sanitized := sanitizeOutput(string(output))
	display, truncated, removed := truncate(sanitized, maxOutputChars)
	result := ExecResult{
		SessionID:       id,
		WallTime:        wallTime,
		Output:          display,
		OutputPath:      logPath,
		OriginalChars:   len(sanitized),
		Truncated:       truncated,
		TruncationBytes: removed,
	}
	snapshot, backgrounded := entry.transitionToBackground()
	if !backgrounded {
		result.ExitCode = cloneIntPtr(snapshot.ExitCode)
		result.Running = false
		m.releaseEntry(id)
		return result, nil
	}
	result.Backgrounded = true
	result.Running = true
	result.MovedToBackground = true
	m.emitEvent(Event{Type: EventBackgrounded, Snapshot: snapshot})
	return result, nil
}

func (m *Manager) WriteStdin(ctx context.Context, req WriteRequest) (ExecResult, error) {
	id := strings.TrimSpace(req.SessionID)
	if id == "" {
		return ExecResult{}, errors.New("session_id is required")
	}
	entry, err := m.entry(id)
	if err != nil {
		return ExecResult{}, err
	}
	entry.interactMu.Lock()
	defer entry.interactMu.Unlock()
	yieldTime := clampYieldTime(req.YieldTime, defaultWriteYieldTime)
	maxOutputChars := normalizeOutputChars(req.MaxOutputChars)
	if req.Input != "" {
		entry.mu.Lock()
		stdin := entry.stdin
		running := entry.running
		stdinOpen := entry.stdinOpen
		entry.mu.Unlock()
		if !running {
			return ExecResult{}, fmt.Errorf("unknown session_id %s", id)
		}
		if stdin == nil || !stdinOpen {
			return ExecResult{}, fmt.Errorf("stdin is closed for session %s", id)
		}
		if _, err := io.WriteString(stdin, req.Input); err != nil {
			return ExecResult{}, fmt.Errorf("write stdin: %w", err)
		}
	}
	start := time.Now()
	output, err := m.collectUntil(ctx, entry, time.Now().Add(yieldTime))
	if err != nil {
		return ExecResult{}, err
	}
	snapshot := entry.snapshot()
	sanitized := sanitizeOutput(string(output))
	display, truncated, removed := truncate(sanitized, maxOutputChars)
	if snapshot.Backgrounded && snapshot.ExitCode != nil {
		entry.markCompletionNoticeConsumed()
	}
	result := ExecResult{
		SessionID:       id,
		WallTime:        time.Since(start),
		Output:          display,
		OutputPath:      snapshot.LogPath,
		OriginalChars:   len(sanitized),
		Truncated:       truncated,
		TruncationBytes: removed,
		Running:         snapshot.Running,
		Backgrounded:    snapshot.Backgrounded,
		ExitCode:        cloneIntPtr(snapshot.ExitCode),
	}
	return result, nil
}

func (m *Manager) Kill(id string) error {
	entry, err := m.entry(id)
	if err != nil {
		return err
	}
	entry.mu.Lock()
	entry.killRequested = true
	process := entry.cmd.Process
	entry.mu.Unlock()
	if process == nil {
		return fmt.Errorf("unknown session_id %s", id)
	}
	if err := killManagedProcess(process); err != nil {
		return err
	}
	return nil
}

func (m *Manager) InlineOutput(id string, maxChars int) (string, string, error) {
	entry, err := m.entry(id)
	if err != nil {
		return "", "", err
	}
	preview, removed, err := readPreviewFromFile(entry.snapshot().LogPath, normalizeOutputChars(maxChars))
	if err != nil {
		return "", "", err
	}
	if removed > 0 {
		return preview, entry.snapshot().LogPath, nil
	}
	return preview, entry.snapshot().LogPath, nil
}

func (m *Manager) List() []Snapshot {
	m.mu.Lock()
	entries := make([]*processEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	m.mu.Unlock()
	out := make([]Snapshot, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.snapshot())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Running != out[j].Running {
			return out[i].Running
		}
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (m *Manager) Count() int {
	m.mu.Lock()
	entries := make([]*processEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	m.mu.Unlock()
	count := 0
	for _, entry := range entries {
		if entry.isRunning() {
			count++
		}
	}
	return count
}

func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	entries := make([]*processEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	m.mu.Unlock()
	for _, entry := range entries {
		entry.mu.Lock()
		if entry.stdin != nil {
			_ = entry.stdin.Close()
			entry.stdin = nil
			entry.stdinOpen = false
		}
		entry.mu.Unlock()
	}
	for _, entry := range entries {
		entry.mu.Lock()
		process := entry.cmd.Process
		entry.mu.Unlock()
		if process != nil {
			_ = killManagedProcess(process)
		}
	}
	graceDeadline := time.Now().Add(closeGracePeriod)
	for _, entry := range entries {
		if waitForEntryDone(entry, time.Until(graceDeadline)) {
			continue
		}
		entry.mu.Lock()
		process := entry.cmd.Process
		entry.mu.Unlock()
		if process != nil {
			_ = forceKillManagedProcess(process)
		}
	}
	deadline := time.Now().Add(closeWaitTimeout)
	pending := make([]string, 0)
	for _, entry := range entries {
		if waitForEntryDone(entry, time.Until(deadline)) {
			continue
		}
		pending = append(pending, entry.id)
	}
	if len(pending) > 0 {
		return fmt.Errorf("timed out waiting for background shells to exit: %s", strings.Join(pending, ", "))
	}
	return nil
}

func (m *Manager) entry(id string) (*processEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.entries[id]
	if !ok {
		return nil, fmt.Errorf("unknown session_id %s", id)
	}
	return entry, nil
}

func (m *Manager) emitEvent(evt Event) {
	m.mu.Lock()
	handler := m.onEvent
	m.mu.Unlock()
	if handler != nil {
		handler(evt)
	}
}

func (m *Manager) waitForExit(entry *processEntry) {
	defer close(entry.done)
	err := entry.cmd.Wait()
	exitCode, state := processExitState(err)
	if !entry.isBackgrounded() {
		entry.setExited(exitCode, state)
		m.releaseEntry(entry.id)
		return
	}
	snapshot := entry.closeOnExit(exitCode, state)
	preview, removed, previewErr := readPreviewFromFile(entry.logPath, defaultLimit)
	if previewErr != nil {
		preview = fmt.Sprintf("failed to read output preview: %v", previewErr)
		removed = 0
	}
	eventType := EventCompleted
	if state == "killed" {
		eventType = EventKilled
	}
	entry.interactMu.Lock()
	noticeSuppressed := entry.completionNoticeConsumed()
	entry.interactMu.Unlock()
	m.emitEvent(Event{Type: eventType, Snapshot: snapshot, Preview: preview, Removed: removed, NoticeSuppressed: noticeSuppressed})
	entry.finalizeClosedExit()
}

func (m *Manager) collectUntil(ctx context.Context, entry *processEntry, deadline time.Time) ([]byte, error) {
	var collected bytes.Buffer
	for {
		pending := entry.drainPending()
		if len(pending) > 0 {
			_, _ = collected.Write(pending)
		}
		if !entry.isRunning() {
			return collected.Bytes(), nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return collected.Bytes(), nil
		}
		select {
		case <-ctx.Done():
			return collected.Bytes(), ctx.Err()
		case <-entry.notify:
		case <-time.After(remaining):
			return collected.Bytes(), nil
		}
	}
}

func (m *Manager) allocateProcessSlot() (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return "", "", errors.New("background shell manager is closed")
	}
	id := strconv.Itoa(m.nextID)
	m.nextID++
	logPath := filepath.Join(m.tempDir, id+".log")
	return id, logPath, nil
}

func (m *Manager) releaseEntry(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, id)
}

func cloneIntPtr(in *int) *int {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func normalizeOutputChars(maxChars int) int {
	if maxChars <= 0 {
		return defaultOutputTokenCap * 4
	}
	return maxChars
}

func clampYieldTime(value time.Duration, fallback time.Duration) time.Duration {
	if value <= 0 {
		value = fallback
	}
	if value < minYieldTime {
		return minYieldTime
	}
	if value > maxYieldTime {
		return maxYieldTime
	}
	return value
}

func waitForEntryDone(entry *processEntry, timeout time.Duration) bool {
	if entry == nil {
		return true
	}
	if timeout <= 0 {
		select {
		case <-entry.done:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-entry.done:
		return true
	case <-timer.C:
		return false
	}
}

func readPreviewFromFile(path string, maxChars int) (string, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	sanitized := sanitizeOutput(string(data))
	preview, truncated, removed := truncate(sanitized, normalizeOutputChars(maxChars))
	if !truncated {
		removed = 0
	}
	return preview, removed, nil
}

func NormalizeBackgroundOutputMode(raw string) BackgroundOutputMode {
	switch BackgroundOutputMode(strings.ToLower(strings.TrimSpace(raw))) {
	case BackgroundOutputVerbose:
		return BackgroundOutputVerbose
	case BackgroundOutputConcise:
		return BackgroundOutputConcise
	default:
		return BackgroundOutputDefault
	}
}

func SummarizeBackgroundEvent(evt Event, opts BackgroundNoticeOptions) BackgroundNoticeSummary {
	maxChars := normalizeOutputChars(opts.MaxChars)
	mode := effectiveBackgroundOutputMode(evt.Snapshot.ExitCode, opts.SuccessOutputMode)
	preview, lineCount, truncated := backgroundNoticePreview(evt, maxChars, mode)
	state := strings.TrimSpace(evt.Snapshot.State)
	if state == "" {
		state = strings.TrimSpace(string(evt.Type))
	}
	if state == "" {
		state = "completed"
	}
	detail := []string{fmt.Sprintf("Background shell %s %s.", evt.Snapshot.ID, state)}
	if evt.Snapshot.ExitCode != nil {
		detail = append(detail, fmt.Sprintf("Exit code: %d", *evt.Snapshot.ExitCode))
	}
	if strings.TrimSpace(evt.Snapshot.LogPath) != "" && lineCount >= 0 {
		detail = append(detail, fmt.Sprintf("Output file (%s): %s", formatOutputLineCount(lineCount), evt.Snapshot.LogPath))
	}
	if mode != BackgroundOutputConcise {
		if strings.TrimSpace(preview) == "" {
			detail = append(detail, "no output")
		} else {
			detail = append(detail, "Output:")
			detail = append(detail, preview)
		}
	}
	ongoing := fmt.Sprintf("Background shell %s %s", evt.Snapshot.ID, state)
	if evt.Snapshot.ExitCode != nil {
		ongoing = fmt.Sprintf("%s (exit %d)", ongoing, *evt.Snapshot.ExitCode)
	}
	return BackgroundNoticeSummary{
		DetailText:  strings.Join(detail, "\n"),
		OngoingText: ongoing,
		LineCount:   lineCount,
		Truncated:   truncated,
		LogPath:     evt.Snapshot.LogPath,
	}
}

func effectiveBackgroundOutputMode(exitCode *int, successMode BackgroundOutputMode) BackgroundOutputMode {
	mode := NormalizeBackgroundOutputMode(string(successMode))
	if exitCode == nil {
		return BackgroundOutputDefault
	}
	if *exitCode == 0 {
		return mode
	}
	if mode == BackgroundOutputVerbose {
		return BackgroundOutputVerbose
	}
	return BackgroundOutputDefault
}

func backgroundNoticePreview(evt Event, maxChars int, mode BackgroundOutputMode) (string, int, bool) {
	if strings.TrimSpace(evt.Snapshot.LogPath) != "" {
		preview, lineCount, truncated, err := readBackgroundSummaryFromFile(evt.Snapshot.LogPath, maxChars, mode)
		if err == nil {
			return preview, lineCount, truncated
		}
	}
	if mode == BackgroundOutputConcise {
		return "", 0, false
	}
	preview := sanitizeOutput(evt.Preview)
	truncated := evt.Removed > 0
	if strings.TrimSpace(preview) == "" {
		return "", 0, truncated
	}
	return preview, countOutputLines(preview), truncated
}

func readBackgroundSummaryFromFile(path string, maxChars int, mode BackgroundOutputMode) (string, int, bool, error) {
	fp, err := os.Open(path)
	if err != nil {
		return "", 0, false, err
	}
	defer fp.Close()
	builder := newBackgroundPreviewBuilder(maxChars, mode)
	buf := make([]byte, 32*1024)
	for {
		n, readErr := fp.Read(buf)
		if n > 0 {
			builder.WriteRaw(buf[:n])
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, io.EOF) {
			builder.Finish()
			return builder.Preview(), builder.LineCount(), builder.Truncated(), nil
		}
		return "", 0, false, readErr
	}
}

func formatOutputLineCount(count int) string {
	if count == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", count)
}

type backgroundPreviewBuilder struct {
	maxChars    int
	mode        BackgroundOutputMode
	carry       []byte
	prevCR      bool
	totalBytes  int
	lineCount   int
	hasContent  bool
	lastNewline bool
	fullMode    bool
	full        []byte
	head        []byte
	tail        []byte
}

func newBackgroundPreviewBuilder(maxChars int, mode BackgroundOutputMode) *backgroundPreviewBuilder {
	maxChars = normalizeOutputChars(maxChars)
	mode = NormalizeBackgroundOutputMode(string(mode))
	return &backgroundPreviewBuilder{
		maxChars: maxChars,
		mode:     mode,
		fullMode: mode == BackgroundOutputVerbose,
		full:     make([]byte, 0, min(maxChars, 4096)),
		head:     make([]byte, 0, headTailSize),
		tail:     make([]byte, 0, headTailSize),
	}
}

func (b *backgroundPreviewBuilder) WriteRaw(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	data := append(append([]byte(nil), b.carry...), chunk...)
	processUpTo := len(data)
	if start, ok := trailingIncompleteANSIStart(data); ok {
		processUpTo = start
		b.carry = append(b.carry[:0], data[start:]...)
	} else {
		b.carry = b.carry[:0]
	}
	if processUpTo == 0 {
		return
	}
	b.writeSanitized(xansi.Strip(string(data[:processUpTo])))
}

func (b *backgroundPreviewBuilder) Finish() {
	if len(b.carry) == 0 {
		return
	}
	b.writeSanitized(xansi.Strip(string(b.carry)))
	b.carry = b.carry[:0]
}

func (b *backgroundPreviewBuilder) writeSanitized(text string) {
	if text == "" {
		return
	}
	for _, r := range text {
		switch {
		case r == '\r':
			b.emitByte('\n')
			b.prevCR = true
		case r == '\n':
			if b.prevCR {
				b.prevCR = false
				continue
			}
			b.emitByte('\n')
		case r == '\t' || !unicode.IsControl(r):
			b.prevCR = false
			var buf [4]byte
			n := utf8EncodeRune(buf[:], r)
			b.emitBytes(buf[:n])
		default:
			b.prevCR = false
		}
	}
}

func (b *backgroundPreviewBuilder) emitByte(v byte) {
	b.emitBytes([]byte{v})
}

func (b *backgroundPreviewBuilder) emitBytes(data []byte) {
	if len(data) == 0 {
		return
	}
	b.hasContent = true
	b.totalBytes += len(data)
	if b.fullMode {
		b.full = append(b.full, data...)
	} else if len(b.full) < b.maxChars {
		remaining := b.maxChars - len(b.full)
		if remaining > len(data) {
			remaining = len(data)
		}
		b.full = append(b.full, data[:remaining]...)
	}
	if len(b.head) < headTailSize {
		remaining := headTailSize - len(b.head)
		if remaining > len(data) {
			remaining = len(data)
		}
		b.head = append(b.head, data[:remaining]...)
	}
	b.tail = append(b.tail, data...)
	if len(b.tail) > headTailSize {
		b.tail = append([]byte(nil), b.tail[len(b.tail)-headTailSize:]...)
	}
	for _, v := range data {
		if v == '\n' {
			b.lineCount++
			b.lastNewline = true
			continue
		}
		b.lastNewline = false
	}
}

func (b *backgroundPreviewBuilder) Preview() string {
	if b.mode == BackgroundOutputConcise {
		return ""
	}
	if b.fullMode {
		return string(b.full)
	}
	if b.totalBytes <= b.maxChars {
		return string(b.full)
	}
	headLen, tailLen := truncationSegmentLengths(b.totalBytes, b.maxChars)
	removed := b.totalBytes - headLen - tailLen
	head := string(b.head[:headLen])
	tail := string(b.tail[len(b.tail)-tailLen:])
	return formatTruncatedPreview(head, removed, tail)
}

func (b *backgroundPreviewBuilder) LineCount() int {
	if !b.hasContent {
		return 0
	}
	if b.lastNewline {
		return b.lineCount
	}
	return b.lineCount + 1
}

func (b *backgroundPreviewBuilder) Truncated() bool {
	if b.mode == BackgroundOutputConcise || b.fullMode {
		return false
	}
	return b.totalBytes > b.maxChars
}

func trailingIncompleteANSIStart(data []byte) (int, bool) {
	lastESC := bytes.LastIndexByte(data, 0x1b)
	if lastESC < 0 {
		return 0, false
	}
	for i := lastESC + 1; i < len(data); i++ {
		if isANSITerminator(data[i]) {
			return 0, false
		}
	}
	return lastESC, true
}

func isANSITerminator(v byte) bool {
	if v == 0x07 {
		return true
	}
	return v >= 0x40 && v <= 0x7e
}

func utf8EncodeRune(dst []byte, r rune) int {
	if r < 0x80 {
		dst[0] = byte(r)
		return 1
	}
	return copy(dst, []byte(string(r)))
}

func countOutputLines(text string) int {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

func formatExecResponse(result ExecResult) string {
	sections := make([]string, 0, 6)
	if result.MovedToBackground {
		sections = append(sections, "Process moved to background.")
	}
	if result.ExitCode != nil {
		sections = append(sections, fmt.Sprintf("Process exited with code %d", *result.ExitCode))
	} else if strings.TrimSpace(result.SessionID) != "" {
		sections = append(sections, fmt.Sprintf("Process running with session ID %s", result.SessionID))
	}
	if result.Backgrounded && result.ExitCode != nil {
		sections = append(sections, fmt.Sprintf("Wall time: %.4f seconds", result.WallTime.Seconds()))
	}
	if result.Backgrounded && result.ExitCode != nil && strings.TrimSpace(result.OutputPath) != "" {
		sections = append(sections, fmt.Sprintf("Log file: %s", result.OutputPath))
	}
	if result.Truncated {
		sections = append(sections, fmt.Sprintf("Original token count: %d", approxTokenCount(result.OriginalChars)))
	}
	sections = append(sections, "Output:")
	sections = append(sections, result.Output)
	return strings.Join(sections, "\n")
}

func approxTokenCount(chars int) int {
	if chars <= 0 {
		return 0
	}
	return int(math.Ceil(float64(chars) / 4.0))
}
