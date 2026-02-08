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
	"strings"

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

type Tool struct {
	workspaceRoot     string
	workspaceRootReal string
	workspaceOnly     bool
}

func New(workspaceRoot string, workspaceOnly bool) (*Tool, error) {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace real path: %w", err)
	}
	return &Tool{workspaceRoot: abs, workspaceRootReal: real, workspaceOnly: workspaceOnly}, nil
}

func (t *Tool) Name() tools.ID {
	return tools.ToolPatch
}

func (t *Tool) Call(_ context.Context, c tools.Call) (tools.Result, error) {
	var in input
	if err := json.Unmarshal(c.Input, &in); err != nil {
		return resultErr(c, fmt.Sprintf("invalid input: %v", err)), nil
	}
	if strings.TrimSpace(in.Patch) == "" {
		return resultErr(c, "patch is required"), nil
	}

	doc, err := parse(in.Patch)
	if err != nil {
		return resultErr(c, err.Error()), nil
	}
	if err := t.apply(doc); err != nil {
		return resultErr(c, err.Error()), nil
	}

	body, _ := json.Marshal(map[string]any{
		"ok":         true,
		"operations": len(doc.Hunks),
	})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body}, nil
}

func (t *Tool) apply(doc Document) error {
	state := map[string]*patchFileState{}

	getState := func(path string) (*patchFileState, error) {
		resolved, err := t.resolvePath(path, false)
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
			target, err := t.resolvePath(op.Path, false)
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
			return errors.New("delete file operation is not allowed")
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
				moveTarget, err := t.resolvePath(op.MoveTo, false)
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

	for _, s := range state {
		if !s.Exists {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(s.NewPath), 0o755); err != nil {
			return fmt.Errorf("create parent dir: %w", err)
		}
	}

	for _, s := range state {
		if !s.Exists {
			continue
		}
		text := strings.Join(s.Content, "\n")
		if len(s.Content) > 0 && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		tmp := s.NewPath + ".builder.tmp"
		if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
			return fmt.Errorf("stage write %s: %w", s.NewPath, err)
		}
	}

	for _, s := range state {
		if !s.Exists {
			continue
		}
		tmp := s.NewPath + ".builder.tmp"
		if err := os.Rename(tmp, s.NewPath); err != nil {
			return fmt.Errorf("commit write %s: %w", s.NewPath, err)
		}
		if s.NewPath != s.Original {
			if err := os.Remove(s.Original); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove moved source %s: %w", s.Original, err)
			}
		}
	}

	return nil
}

func (t *Tool) resolvePath(path string, mustExist bool) (string, error) {
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
		rel, err := filepath.Rel(t.workspaceRootReal, real)
		if err != nil {
			return "", fmt.Errorf("rel path check for %q: %w", path, err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("patch target outside workspace: %s", path)
		}
	}
	return real, nil
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
	out := make([]string, 0, len(original)+len(changes)/2)
	i := 0
	for _, ch := range changes {
		switch ch.Kind {
		case '@':
			continue
		case ' ':
			idx := indexOf(original, i, ch.Content)
			if idx < 0 {
				return nil, fmt.Errorf("context line not found: %q", ch.Content)
			}
			out = append(out, original[i:idx+1]...)
			i = idx + 1
		case '-':
			if i >= len(original) {
				return nil, fmt.Errorf("delete past end: %q", ch.Content)
			}
			if original[i] == ch.Content {
				i++
				continue
			}
			idx := indexOf(original, i, ch.Content)
			if idx < 0 {
				return nil, fmt.Errorf("delete line not found: %q", ch.Content)
			}
			out = append(out, original[i:idx]...)
			i = idx + 1
		case '+':
			out = append(out, ch.Content)
		default:
			return nil, fmt.Errorf("unknown change line prefix %q", string(ch.Kind))
		}
	}
	out = append(out, original[i:]...)
	return out, nil
}

func indexOf(lines []string, start int, want string) int {
	for i := start; i < len(lines); i++ {
		if lines[i] == want {
			return i
		}
	}
	return -1
}

func resultErr(c tools.Call, msg string) tools.Result {
	body, _ := json.Marshal(map[string]any{"error": msg})
	return tools.Result{CallID: c.ID, Name: c.Name, Output: body, IsError: true}
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
