package runprompt

import (
	"strings"
	"sync"
	"time"

	"builder/server/runtime"
	shelltool "builder/server/tools/shell"
)

type BackgroundEventRouter struct {
	mu              sync.RWMutex
	activeSessionID string
	activeSince     time.Time
	activeEngine    *runtime.Engine
	outputLimit     int
	outputMode      shelltool.BackgroundOutputMode
}

func NewBackgroundEventRouter(background *shelltool.Manager, outputLimit int, outputMode shelltool.BackgroundOutputMode) *BackgroundEventRouter {
	router := &BackgroundEventRouter{outputLimit: outputLimit, outputMode: outputMode}
	if background != nil {
		background.SetEventHandler(router.handle)
	}
	return router
}

func (r *BackgroundEventRouter) SetActiveSession(sessionID string, engine *runtime.Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeSessionID = strings.TrimSpace(sessionID)
	r.activeSince = time.Now().UTC()
	r.activeEngine = engine
}

func (r *BackgroundEventRouter) ClearActiveSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.TrimSpace(sessionID) != "" && r.activeSessionID != strings.TrimSpace(sessionID) {
		return
	}
	r.activeSessionID = ""
	r.activeSince = time.Time{}
	r.activeEngine = nil
}

func (r *BackgroundEventRouter) handle(evt shelltool.Event) {
	r.mu.RLock()
	activeSessionID := r.activeSessionID
	activeSince := r.activeSince
	activeEngine := r.activeEngine
	outputLimit := r.outputLimit
	outputMode := r.outputMode
	r.mu.RUnlock()
	if activeEngine == nil {
		return
	}
	summary := shelltool.BackgroundNoticeSummary{}
	if evt.Type == shelltool.EventCompleted || evt.Type == shelltool.EventKilled {
		summary = shelltool.SummarizeBackgroundEvent(evt, shelltool.BackgroundNoticeOptions{
			MaxChars:          outputLimit,
			SuccessOutputMode: outputMode,
		})
	}
	ownerSessionID := strings.TrimSpace(evt.Snapshot.OwnerSessionID)
	shouldNotify := ownerSessionID != "" && ownerSessionID == activeSessionID && !evt.NoticeSuppressed
	if shouldNotify && !evt.Snapshot.FinishedAt.IsZero() && evt.Snapshot.FinishedAt.Before(activeSince) {
		shouldNotify = false
	}
	activeEngine.HandleBackgroundShellUpdate(runtime.BackgroundShellEvent{
		Type:              string(evt.Type),
		ID:                evt.Snapshot.ID,
		State:             evt.Snapshot.State,
		Command:           evt.Snapshot.Command,
		Workdir:           evt.Snapshot.Workdir,
		LogPath:           evt.Snapshot.LogPath,
		NoticeText:        summary.DetailText,
		CompactText:       summary.OngoingText,
		Preview:           evt.Preview,
		Removed:           evt.Removed,
		ExitCode:          cloneIntPtr(evt.Snapshot.ExitCode),
		UserRequestedKill: evt.Snapshot.KillRequested,
		NoticeSuppressed:  evt.NoticeSuppressed,
	}, shouldNotify)
}

func cloneIntPtr(v *int) *int {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}
