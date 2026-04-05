package patch

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"builder/shared/textutil"
)

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
	s = textutil.NormalizeCRLF(s)
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
