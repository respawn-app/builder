package shell

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"builder/internal/tokenutil"
)

const (
	defaultMinimumExecToBgTime = 15 * time.Second
	defaultWriteYieldTime      = 250 * time.Millisecond
	closeGracePeriod           = 1 * time.Second
	closeWaitTimeout           = 5 * time.Second
	minWriteYieldTime          = 250 * time.Millisecond
	defaultOutputTokenCap      = 10_000
	maxPendingOutputBytes      = 1 << 20
	maxRecentPreviewBytes      = 4096
	backgroundLogDirPrefix     = "builder-bg-shells-"
	initialProcessID           = 1000
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
	Warning           string
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

func (p *processEntry) snapshotLocked() Snapshot {
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

func (p *processEntry) closeResourcesLocked() {
	if p.stdin != nil {
		_ = p.stdin.Close()
		p.stdin = nil
	}
	if p.logFile != nil {
		_ = p.logFile.Sync()
		_ = p.logFile.Close()
		p.logFile = nil
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
	p.closeResourcesLocked()
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
	p.running = false
	p.finishedAt = time.Now().UTC()
	p.lastUpdatedAt = p.finishedAt
	p.exitCode = &exitCode
	p.state = state
	p.closeResourcesLocked()
	p.signal()
	return p.snapshotLocked()
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
	return p.snapshotLocked()
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
		return p.snapshotLocked(), false
	}
	p.backgrounded = true
	p.state = "running"
	return p.snapshotLocked(), true
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

func normalizeWriteYieldTime(value time.Duration, fallback time.Duration) time.Duration {
	if value <= 0 {
		value = fallback
	}
	if value < minWriteYieldTime {
		return minWriteYieldTime
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

func approxTokenCount(chars int) int {
	return tokenutil.ApproxTokenCount(chars)
}

func countOutputLines(text string) int {
	trimmed := strings.TrimRight(text, "\n")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}
