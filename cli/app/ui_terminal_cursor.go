package app

import (
	"io"
	"sync"

	xansi "github.com/charmbracelet/x/ansi"
)

type uiInputFieldCursor struct {
	Visible bool
	Row     int
	Col     int
}

type uiTerminalCursorPlacement struct {
	Visible   bool
	CursorRow int
	CursorCol int
	AnchorRow int
	AltScreen bool
}

type uiTerminalCursorState struct {
	mu       sync.Mutex
	latest   uiTerminalCursorPlacement
	previous uiTerminalCursorPlacement
	placed   bool
}

func newUITerminalCursorState() *uiTerminalCursorState {
	return &uiTerminalCursorState{}
}

func (s *uiTerminalCursorState) Set(placement uiTerminalCursorPlacement) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = sanitizeTerminalCursorPlacement(placement)
}

func (s *uiTerminalCursorState) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = uiTerminalCursorPlacement{}
}

func (s *uiTerminalCursorState) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = uiTerminalCursorPlacement{}
	s.previous = uiTerminalCursorPlacement{}
	s.placed = false
}

func (s *uiTerminalCursorState) Snapshot() (uiTerminalCursorPlacement, bool) {
	if s == nil {
		return uiTerminalCursorPlacement{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.latest.Visible {
		return s.latest, false
	}
	return s.latest, true
}

func (s *uiTerminalCursorState) hasPlacement() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.placed
}

func (s *uiTerminalCursorState) restoreRendererAnchor() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.placed {
		return ""
	}
	return terminalCursorRestoreSequence(s.previous)
}

func (s *uiTerminalCursorState) placeCursor() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	placement := sanitizeTerminalCursorPlacement(s.latest)
	s.previous = placement
	s.placed = placement.Visible
	if !placement.Visible {
		return ""
	}
	return terminalCursorPlaceSequence(placement)
}

func sanitizeTerminalCursorPlacement(placement uiTerminalCursorPlacement) uiTerminalCursorPlacement {
	if placement.CursorRow < 0 {
		placement.CursorRow = 0
	}
	if placement.CursorCol < 0 {
		placement.CursorCol = 0
	}
	if placement.AnchorRow < 0 {
		placement.AnchorRow = 0
	}
	return placement
}

func terminalCursorRestoreSequence(placement uiTerminalCursorPlacement) string {
	placement = sanitizeTerminalCursorPlacement(placement)
	if placement.AltScreen {
		return xansi.CursorPosition(1, placement.AnchorRow+1)
	}
	rowsDown := placement.AnchorRow - placement.CursorRow
	if rowsDown < 0 {
		rowsDown = 0
	}
	if rowsDown == 0 {
		return "\r"
	}
	return xansi.CursorDown(rowsDown) + "\r"
}

func terminalCursorPlaceSequence(placement uiTerminalCursorPlacement) string {
	placement = sanitizeTerminalCursorPlacement(placement)
	if !placement.Visible {
		return ansiHideCursor
	}
	if placement.AltScreen {
		return xansi.ShowCursor + xansi.CursorPosition(placement.CursorCol+1, placement.CursorRow+1)
	}
	rowsUp := placement.AnchorRow - placement.CursorRow
	if rowsUp < 0 {
		rowsUp = 0
	}
	sequence := xansi.ShowCursor
	if rowsUp > 0 {
		sequence += xansi.CursorUp(rowsUp)
	}
	if placement.CursorCol > 0 {
		sequence += xansi.CursorForward(placement.CursorCol)
	}
	return sequence
}

type uiTerminalCursorWriter struct {
	out   io.Writer
	state *uiTerminalCursorState
}

func newUITerminalCursorWriter(out io.Writer, state *uiTerminalCursorState) io.Writer {
	if out == nil || state == nil {
		return out
	}
	return uiTerminalCursorWriter{out: out, state: state}
}

func (w uiTerminalCursorWriter) Write(p []byte) (int, error) {
	shouldPreserveCursor := w.state.hasPlacement()
	if shouldPreserveCursor {
		if prefix := w.state.restoreRendererAnchor(); prefix != "" {
			if _, err := io.WriteString(w.out, prefix); err != nil {
				return 0, err
			}
		}
	}
	n, err := w.out.Write(p)
	if err != nil {
		return n, err
	}
	if shouldPreserveCursor || len(p) > 0 {
		if suffix := w.state.placeCursor(); suffix != "" {
			if _, err := io.WriteString(w.out, suffix); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}
