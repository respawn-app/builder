package patch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"builder/internal/tools"
)

type input struct {
	Patch string `json:"patch"`
}

type patchFileState struct {
	Exists   bool
	Content  []string
	NewPath  string
	Original string
}

type fileSnapshot struct {
	Exists bool
	Mode   os.FileMode
	Data   []byte
}

type committedWrite struct {
	Path   string
	Before fileSnapshot
}

type removedSource struct {
	Path   string
	Before fileSnapshot
}

type OutsideWorkspaceRequest struct {
	RequestedPath string
	ResolvedPath  string
	WorkspaceRoot string
}

type OutsideWorkspaceDecision int

const (
	OutsideWorkspaceDecisionDeny OutsideWorkspaceDecision = iota
	OutsideWorkspaceDecisionAllowOnce
	OutsideWorkspaceDecisionAllowSession
)

type OutsideWorkspaceApproval struct {
	Decision   OutsideWorkspaceDecision
	Commentary string
}

type OutsideWorkspaceApprover func(context.Context, OutsideWorkspaceRequest) (OutsideWorkspaceApproval, error)

type Option func(*Tool)

func WithAllowOutsideWorkspace(allow bool) Option {
	return func(t *Tool) {
		t.allowOutsideWorkspace = allow
	}
}

func WithOutsideWorkspaceApprover(approver OutsideWorkspaceApprover) Option {
	return func(t *Tool) {
		t.outsideWorkspaceApprover = approver
	}
}

type Tool struct {
	workspaceRoot                string
	workspaceRootReal            string
	workspaceRootInfo            os.FileInfo
	workspaceOnly                bool
	allowOutsideWorkspace        bool
	outsideWorkspaceApprover     OutsideWorkspaceApprover
	outsideWorkspaceSessionMu    sync.RWMutex
	outsideWorkspaceSessionAllow bool
}

const hunkMaxFuzz = 8

const outsideWorkspaceRejectionInstruction = "do not attempt to circumvent this restriction in any way. if it's essential to the task, ask the user to make the edit manually at the end of the task."

var unifiedHunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: .*)?$`)

var (
	temporaryEditableRootsOnce sync.Once
	temporaryEditableRoots     []string
)

type editHunk struct {
	header  hunkHeader
	changes []ChangeLine
}

type hunkHeader struct {
	hasPosition bool
	oldStart    int
	oldCount    int
	newStart    int
	newCount    int
}

func New(workspaceRoot string, workspaceOnly bool, opts ...Option) (*Tool, error) {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace real path: %w", err)
	}
	rootInfo, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("stat workspace root: %w", err)
	}
	t := &Tool{workspaceRoot: abs, workspaceRootReal: real, workspaceRootInfo: rootInfo, workspaceOnly: workspaceOnly}
	for _, opt := range opts {
		if opt != nil {
			opt(t)
		}
	}
	return t, nil
}

func (t *Tool) Name() tools.ID {
	return tools.ToolPatch
}

func (t *Tool) Call(ctx context.Context, c tools.Call) (tools.Result, error) {
	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return tools.ErrorResult(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	if strings.TrimSpace(in.Patch) == "" {
		return tools.ErrorResult(c, "patch is required"), nil
	}

	doc, err := parse(in.Patch)
	if err != nil {
		return tools.ErrorResult(c, err.Error()), nil
	}
	if err := t.apply(ctx, doc); err != nil {
		return tools.ErrorResult(c, err.Error()), nil
	}

	body, _ := json.Marshal(map[string]any{
		"ok":         true,
		"operations": len(doc.Hunks),
	})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body}, nil
}

func (t *Tool) apply(ctx context.Context, doc Document) error {
	state := map[string]*patchFileState{}
	approvedOutside := map[string]bool{}

	getState := func(path string) (*patchFileState, error) {
		resolved, err := t.resolvePath(ctx, path, false, approvedOutside)
		if err != nil {
			return nil, err
		}
		if s, ok := state[resolved]; ok {
			return s, nil
		}
		s := &patchFileState{NewPath: resolved, Original: resolved}
		data, err := os.ReadFile(resolved)
		if err == nil {
			s.Exists = true
			s.Content = splitLines(string(data))
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read file %q: %w", path, err)
		}
		state[resolved] = s
		return s, nil
	}

	for _, h := range doc.Hunks {
		switch op := h.(type) {
		case AddFile:
			target, err := t.resolvePath(ctx, op.Path, false, approvedOutside)
			if err != nil {
				return err
			}
			if _, exists := state[target]; exists {
				return fmt.Errorf("add file target already referenced: %s", op.Path)
			}
			if _, err := os.Stat(target); err == nil {
				return fmt.Errorf("add file target already exists: %s", op.Path)
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("stat add target: %w", err)
			}
			state[target] = &patchFileState{
				Exists:   true,
				Content:  append([]string(nil), op.Content...),
				NewPath:  target,
				Original: target,
			}
		case DeleteFile:
			return errors.New("deleting files via apply_patch tool is not allowed; use shell tools")
		case UpdateFile:
			s, err := getState(op.Path)
			if err != nil {
				return err
			}
			if !s.Exists {
				return fmt.Errorf("update target does not exist: %s", op.Path)
			}
			updated, err := applyEdit(s.Content, op.Changes)
			if err != nil {
				return fmt.Errorf("apply update %s: %w", op.Path, err)
			}
			s.Content = updated
			if op.MoveTo != "" {
				moveTarget, err := t.resolvePath(ctx, op.MoveTo, false, approvedOutside)
				if err != nil {
					return err
				}
				if moveTarget != s.Original {
					if _, ok := state[moveTarget]; ok {
						return fmt.Errorf("move target already referenced: %s", op.MoveTo)
					}
					if _, err := os.Stat(moveTarget); err == nil {
						return fmt.Errorf("move target already exists: %s", op.MoveTo)
					} else if !errors.Is(err, os.ErrNotExist) {
						return fmt.Errorf("stat move target: %w", err)
					}
					delete(state, s.NewPath)
					s.NewPath = moveTarget
					state[moveTarget] = s
				}
			}
		default:
			return fmt.Errorf("unsupported patch hunk: %T", h)
		}
	}

	states := sortedActiveStates(state)
	for _, s := range states {
		if err := os.MkdirAll(filepath.Dir(s.NewPath), 0o755); err != nil {
			return fmt.Errorf("create parent dir: %w", err)
		}
	}

	for _, s := range states {
		text := strings.Join(s.Content, "\n")
		if len(s.Content) > 0 && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		tmp := stagedPath(s.NewPath)
		if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
			return fmt.Errorf("stage write %s: %w", s.NewPath, err)
		}
	}
	defer cleanupStagedFiles(states)

	if err := commitStagedFiles(states); err != nil {
		return err
	}

	return nil
}

func sortedActiveStates(state map[string]*patchFileState) []*patchFileState {
	out := make([]*patchFileState, 0, len(state))
	for _, s := range state {
		if s.Exists {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].NewPath < out[j].NewPath
	})
	return out
}

func stagedPath(path string) string {
	return path + ".builder.tmp"
}

func cleanupStagedFiles(states []*patchFileState) {
	for _, s := range states {
		_ = os.Remove(stagedPath(s.NewPath))
	}
}

func commitStagedFiles(states []*patchFileState) error {
	committed := make([]committedWrite, 0, len(states))
	removed := make([]removedSource, 0, len(states))
	rollback := func() error {
		var rollbackErr error
		for i := len(committed) - 1; i >= 0; i-- {
			entry := committed[i]
			if err := restoreSnapshot(entry.Path, entry.Before); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore target %s: %w", entry.Path, err))
			}
		}
		for i := len(removed) - 1; i >= 0; i-- {
			entry := removed[i]
			if err := restoreSnapshot(entry.Path, entry.Before); err != nil {
				rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore moved source %s: %w", entry.Path, err))
			}
		}
		return rollbackErr
	}

	for _, s := range states {
		before, err := captureSnapshot(s.NewPath)
		if err != nil {
			return withRollback(fmt.Errorf("snapshot target %s: %w", s.NewPath, err), rollback())
		}
		if err := os.Rename(stagedPath(s.NewPath), s.NewPath); err != nil {
			return withRollback(fmt.Errorf("commit write %s: %w", s.NewPath, err), rollback())
		}
		committed = append(committed, committedWrite{Path: s.NewPath, Before: before})
	}

	for _, s := range states {
		if s.NewPath == s.Original {
			continue
		}
		before, err := captureSnapshot(s.Original)
		if err != nil {
			return withRollback(fmt.Errorf("snapshot moved source %s: %w", s.Original, err), rollback())
		}
		if !before.Exists {
			continue
		}
		if err := os.Remove(s.Original); err != nil {
			return withRollback(fmt.Errorf("remove moved source %s: %w", s.Original, err), rollback())
		}
		removed = append(removed, removedSource{Path: s.Original, Before: before})
	}

	return nil
}

func captureSnapshot(path string) (fileSnapshot, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return fileSnapshot{}, fmt.Errorf("not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return fileSnapshot{Exists: true, Mode: info.Mode().Perm(), Data: data}, nil
}

func restoreSnapshot(path string, snapshot fileSnapshot) error {
	if !snapshot.Exists {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".builder.rollback.tmp"
	if err := os.WriteFile(tmp, snapshot.Data, snapshot.Mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func withRollback(primary, rollbackErr error) error {
	if rollbackErr == nil {
		return primary
	}
	return errors.Join(primary, fmt.Errorf("rollback failed: %w", rollbackErr))
}

func (t *Tool) resolvePath(ctx context.Context, path string, mustExist bool, approvedOutside map[string]bool) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty path")
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(t.workspaceRoot, candidate)
	}
	candidate = filepath.Clean(candidate)

	real := candidate
	if mustExist {
		var err error
		real, err = filepath.EvalSymlinks(real)
		if err != nil {
			return "", fmt.Errorf("resolve path %q: %w", path, err)
		}
	} else {
		parent := filepath.Dir(candidate)
		if _, err := os.Stat(parent); err == nil {
			parentReal, evalErr := filepath.EvalSymlinks(parent)
			if evalErr != nil {
				return "", fmt.Errorf("resolve parent path for %q: %w", path, evalErr)
			}
			real = filepath.Join(parentReal, filepath.Base(candidate))
		} else if errors.Is(err, os.ErrNotExist) {
			anchor := parent
			for {
				if _, statErr := os.Stat(anchor); statErr == nil {
					break
				} else if !errors.Is(statErr, os.ErrNotExist) {
					return "", fmt.Errorf("stat path anchor for %q: %w", path, statErr)
				}
				next := filepath.Dir(anchor)
				if next == anchor {
					return "", fmt.Errorf("resolve parent path for %q: no existing ancestor", path)
				}
				anchor = next
			}
			anchorReal, evalErr := filepath.EvalSymlinks(anchor)
			if evalErr != nil {
				return "", fmt.Errorf("resolve existing ancestor for %q: %w", path, evalErr)
			}
			relTail, relErr := filepath.Rel(anchor, candidate)
			if relErr != nil {
				return "", fmt.Errorf("build target tail for %q: %w", path, relErr)
			}
			real = filepath.Clean(filepath.Join(anchorReal, relTail))
		} else {
			return "", fmt.Errorf("stat parent path for %q: %w", path, err)
		}
	}

	if t.workspaceOnly {
		insideWorkspace, containmentErr := t.isWithinWorkspace(real)
		if containmentErr != nil {
			return "", fmt.Errorf("workspace boundary check for %q: %w", path, containmentErr)
		}
		if !insideWorkspace {
			if isPathInTemporaryDir(real) {
				return real, nil
			}
			if t.allowOutsideWorkspace || t.outsideWorkspaceSessionAllowed() {
				return real, nil
			}
			if approvedOutside != nil && approvedOutside[real] {
				return real, nil
			}
			if t.outsideWorkspaceApprover == nil {
				return "", fmt.Errorf("patch target outside workspace: %s", path)
			}
			approval, approveErr := t.outsideWorkspaceApprover(ctx, OutsideWorkspaceRequest{
				RequestedPath: path,
				ResolvedPath:  real,
				WorkspaceRoot: t.workspaceRoot,
			})
			if approveErr != nil {
				return "", fmt.Errorf("outside-workspace edit approval failed for %s: %w", path, approveErr)
			}
			switch approval.Decision {
			case OutsideWorkspaceDecisionAllowOnce:
				if approvedOutside != nil {
					approvedOutside[real] = true
				}
				return real, nil
			case OutsideWorkspaceDecisionAllowSession:
				t.setOutsideWorkspaceSessionAllowed(true)
				if approvedOutside != nil {
					approvedOutside[real] = true
				}
				return real, nil
			default:
				errMessage := fmt.Sprintf("patch target outside workspace rejected by user: %s; %s", path, outsideWorkspaceRejectionInstruction)
				commentary := strings.TrimSpace(approval.Commentary)
				if commentary != "" {
					errMessage += fmt.Sprintf(" User commented about this: %s", strconv.Quote(commentary))
				}
				return "", errors.New(errMessage)
			}
		}
	}
	return real, nil
}

func (t *Tool) isWithinWorkspace(real string) (bool, error) {
	rel, relErr := filepath.Rel(t.workspaceRootReal, real)
	if relErr == nil {
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true, nil
		}
	}

	if t.workspaceRootInfo == nil {
		return false, errors.New("workspace root info unavailable")
	}

	current := real
	for {
		info, statErr := os.Stat(current)
		if statErr != nil {
			return false, fmt.Errorf("stat candidate path %q: %w", current, statErr)
		}
		if os.SameFile(info, t.workspaceRootInfo) {
			return true, nil
		}
		next := filepath.Dir(current)
		if next == current {
			break
		}
		current = next
	}

	return false, nil
}

func isPathInTemporaryDir(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	abs := path
	if !filepath.IsAbs(abs) {
		resolvedAbs, err := filepath.Abs(abs)
		if err != nil {
			return false
		}
		abs = resolvedAbs
	}
	abs = filepath.Clean(abs)
	for _, root := range tempEditableRoots() {
		if pathWithinRoot(abs, root) {
			return true
		}
	}
	return false
}

func tempEditableRoots() []string {
	temporaryEditableRootsOnce.Do(func() {
		roots := make([]string, 0, 8)
		add := func(raw string) {
			root := normalizeExistingPath(raw)
			if root == "" {
				return
			}
			roots = append(roots, root)
		}

		add(os.TempDir())
		add(os.Getenv("TMPDIR"))
		add(os.Getenv("TEMP"))
		add(os.Getenv("TMP"))
		if runtime.GOOS != "windows" {
			add("/tmp")
			add("/var/tmp")
			add("/private/tmp")
		}

		seen := make(map[string]struct{}, len(roots))
		deduped := make([]string, 0, len(roots))
		for _, root := range roots {
			if _, ok := seen[root]; ok {
				continue
			}
			seen[root] = struct{}{}
			deduped = append(deduped, root)
		}
		sort.Strings(deduped)
		temporaryEditableRoots = deduped
	})
	out := make([]string, len(temporaryEditableRoots))
	copy(out, temporaryEditableRoots)
	return out
}

func normalizeExistingPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	abs := trimmed
	if !filepath.IsAbs(abs) {
		resolvedAbs, err := filepath.Abs(abs)
		if err != nil {
			return ""
		}
		abs = resolvedAbs
	}
	abs = filepath.Clean(abs)
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real)
	}
	return abs
}

func pathWithinRoot(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (t *Tool) outsideWorkspaceSessionAllowed() bool {
	t.outsideWorkspaceSessionMu.RLock()
	defer t.outsideWorkspaceSessionMu.RUnlock()
	return t.outsideWorkspaceSessionAllow
}

func (t *Tool) setOutsideWorkspaceSessionAllowed(allow bool) {
	t.outsideWorkspaceSessionMu.Lock()
	t.outsideWorkspaceSessionAllow = allow
	t.outsideWorkspaceSessionMu.Unlock()
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func applyEdit(original []string, changes []ChangeLine) ([]string, error) {
	hunks, err := parseEditHunks(changes)
	if err != nil {
		return nil, err
	}

	current := append([]string(nil), original...)
	cumulativeOffset := 0
	searchFloor := 0

	for idx, h := range hunks {
		expected := -1
		if h.header.hasPosition {
			expected = h.header.oldStart - 1 + cumulativeOffset
		}
		anchor, err := findHunkAnchor(current, h.changes, expected, searchFloor, h.header.hasPosition)
		if err != nil {
			return nil, fmt.Errorf("hunk %d: %w", idx+1, err)
		}
		next, oldCount, newCount, err := applyHunkAt(current, h.changes, anchor)
		if err != nil {
			return nil, fmt.Errorf("hunk %d: %w", idx+1, err)
		}
		if h.header.hasPosition {
			if oldCount != h.header.oldCount || newCount != h.header.newCount {
				return nil, fmt.Errorf(
					"hunk %d: header count mismatch: old %d->%d new %d->%d",
					idx+1,
					h.header.oldCount,
					oldCount,
					h.header.newCount,
					newCount,
				)
			}
		}
		current = next
		cumulativeOffset += newCount - oldCount
		searchFloor = anchor + newCount
	}
	return current, nil
}

func parseEditHunks(changes []ChangeLine) ([]editHunk, error) {
	if len(changes) == 0 {
		return nil, nil
	}

	hunks := make([]editHunk, 0, 4)
	current := editHunk{}

	flush := func() error {
		if len(current.changes) == 0 {
			if current.header.hasPosition {
				return errors.New("hunk header without changes")
			}
			return nil
		}
		hunks = append(hunks, current)
		current = editHunk{}
		return nil
	}

	for _, ch := range changes {
		switch ch.Kind {
		case '@':
			if err := flush(); err != nil {
				return nil, err
			}
			header, err := parseHunkHeader("@" + ch.Content)
			if err != nil {
				return nil, err
			}
			current.header = header
		case ' ', '+', '-':
			current.changes = append(current.changes, ch)
		default:
			return nil, fmt.Errorf("unknown change line prefix %q", string(ch.Kind))
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return hunks, nil
}

func parseHunkHeader(line string) (hunkHeader, error) {
	line = strings.TrimSpace(line)
	if line == "@@" {
		return hunkHeader{}, nil
	}

	m := unifiedHunkHeaderPattern.FindStringSubmatch(line)
	if len(m) == 0 {
		return hunkHeader{}, fmt.Errorf("invalid hunk header: %q", line)
	}

	oldStart, err := strconv.Atoi(m[1])
	if err != nil {
		return hunkHeader{}, fmt.Errorf("invalid hunk old start %q: %w", m[1], err)
	}
	oldCount := 1
	if strings.TrimSpace(m[2]) != "" {
		oldCount, err = strconv.Atoi(m[2])
		if err != nil {
			return hunkHeader{}, fmt.Errorf("invalid hunk old count %q: %w", m[2], err)
		}
	}
	newStart, err := strconv.Atoi(m[3])
	if err != nil {
		return hunkHeader{}, fmt.Errorf("invalid hunk new start %q: %w", m[3], err)
	}
	newCount := 1
	if strings.TrimSpace(m[4]) != "" {
		newCount, err = strconv.Atoi(m[4])
		if err != nil {
			return hunkHeader{}, fmt.Errorf("invalid hunk new count %q: %w", m[4], err)
		}
	}

	return hunkHeader{
		hasPosition: true,
		oldStart:    oldStart,
		oldCount:    oldCount,
		newStart:    newStart,
		newCount:    newCount,
	}, nil
}

func findHunkAnchor(lines []string, changes []ChangeLine, expected, floor int, anchored bool) (int, error) {
	if floor < 0 {
		floor = 0
	}
	maxStart := len(lines)
	if floor > maxStart {
		floor = maxStart
	}

	matchAt := func(start int) bool {
		_, _, _, err := applyHunkAt(lines, changes, start)
		return err == nil
	}

	if anchored {
		if expected >= floor && expected <= maxStart && matchAt(expected) {
			return expected, nil
		}
		for fuzz := 1; fuzz <= hunkMaxFuzz; fuzz++ {
			up := expected - fuzz
			if up >= floor && up <= maxStart && matchAt(up) {
				return up, nil
			}
			down := expected + fuzz
			if down >= floor && down <= maxStart && matchAt(down) {
				return down, nil
			}
		}
		return -1, fmt.Errorf("hunk did not match near expected line %d (fuzz %d)", expected+1, hunkMaxFuzz)
	}

	for start := floor; start <= maxStart; start++ {
		if matchAt(start) {
			return start, nil
		}
	}
	return -1, errors.New("hunk did not match file content")
}

func applyHunkAt(lines []string, changes []ChangeLine, start int) ([]string, int, int, error) {
	if start < 0 || start > len(lines) {
		return nil, 0, 0, fmt.Errorf("invalid hunk start %d", start)
	}

	out := make([]string, 0, len(lines)+len(changes))
	out = append(out, lines[:start]...)

	cursor := start
	oldCount := 0
	newCount := 0
	for _, ch := range changes {
		switch ch.Kind {
		case ' ':
			if cursor >= len(lines) || lines[cursor] != ch.Content {
				return nil, 0, 0, fmt.Errorf("context mismatch at line %d: want %q", cursor+1, ch.Content)
			}
			out = append(out, lines[cursor])
			cursor++
			oldCount++
			newCount++
		case '-':
			if cursor >= len(lines) || lines[cursor] != ch.Content {
				return nil, 0, 0, fmt.Errorf("delete mismatch at line %d: want %q", cursor+1, ch.Content)
			}
			cursor++
			oldCount++
		case '+':
			out = append(out, ch.Content)
			newCount++
		default:
			return nil, 0, 0, fmt.Errorf("unknown change line prefix %q", string(ch.Kind))
		}
	}

	out = append(out, lines[cursor:]...)
	return out, oldCount, newCount, nil
}

func parse(src string) (Document, error) {
	s := scanner{lines: splitRawLines(src)}
	if !s.consumeExact("*** Begin Patch") {
		return Document{}, errors.New("patch must start with *** Begin Patch")
	}

	doc := Document{}
	for !s.done() {
		line := s.peek()
		switch {
		case line == "*** End Patch":
			s.next()
			if !s.done() {
				return Document{}, errors.New("unexpected content after *** End Patch")
			}
			return doc, nil
		case strings.HasPrefix(line, "*** Add File: "):
			head := strings.TrimPrefix(s.next(), "*** Add File: ")
			content := []string{}
			for !s.done() {
				n := s.peek()
				if strings.HasPrefix(n, "*** ") {
					break
				}
				if !strings.HasPrefix(n, "+") {
					return Document{}, fmt.Errorf("add file line must start with +: %q", n)
				}
				content = append(content, strings.TrimPrefix(s.next(), "+"))
			}
			doc.Hunks = append(doc.Hunks, AddFile{Path: head, Content: content})
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimPrefix(s.next(), "*** Delete File: ")
			doc.Hunks = append(doc.Hunks, DeleteFile{Path: path})
		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimPrefix(s.next(), "*** Update File: ")
			up := UpdateFile{Path: path}
			if !s.done() && strings.HasPrefix(s.peek(), "*** Move to: ") {
				up.MoveTo = strings.TrimPrefix(s.next(), "*** Move to: ")
			}
			for !s.done() {
				n := s.peek()
				if strings.HasPrefix(n, "*** ") {
					break
				}
				if n == "" {
					up.Changes = append(up.Changes, ChangeLine{Kind: ' ', Content: ""})
					s.next()
					continue
				}
				p := n[0]
				if p != ' ' && p != '+' && p != '-' && p != '@' {
					return Document{}, fmt.Errorf("invalid update line prefix in %q", n)
				}
				up.Changes = append(up.Changes, ChangeLine{Kind: rune(p), Content: n[1:]})
				s.next()
			}
			doc.Hunks = append(doc.Hunks, up)
		default:
			return Document{}, fmt.Errorf("unknown patch block: %q", line)
		}
	}

	return Document{}, errors.New("missing *** End Patch")
}

type scanner struct {
	lines []string
	idx   int
}

func (s *scanner) done() bool {
	return s.idx >= len(s.lines)
}

func (s *scanner) peek() string {
	if s.done() {
		return ""
	}
	return s.lines[s.idx]
}

func (s *scanner) next() string {
	v := s.peek()
	s.idx++
	return v
}

func (s *scanner) consumeExact(v string) bool {
	if s.peek() == v {
		s.next()
		return true
	}
	return false
}

func splitRawLines(in string) []string {
	in = strings.ReplaceAll(in, "\r\n", "\n")
	reader := bufio.NewScanner(bytes.NewBufferString(in))
	out := []string{}
	for reader.Scan() {
		out = append(out, reader.Text())
	}
	return out
}
