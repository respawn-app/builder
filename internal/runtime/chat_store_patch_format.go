package runtime

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"builder/internal/shared/textutil"
	"builder/internal/transcript/toolcodec"
)

func formatRawToolJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if !json.Valid(raw) {
		return strings.TrimSpace(string(raw))
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return strings.TrimSpace(string(raw))
	}
	formatted, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(formatted)
}

type patchFileView struct {
	AbsPath string
	RelPath string
	Added   int
	Removed int
	Diff    []string
}

func (s *chatStore) formatPatchToolCall(raw json.RawMessage) (summary, detail string, ok bool) {
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", "", false
	}
	patchRaw, ok := input["patch"]
	if !ok {
		return "", "", false
	}
	var patchText string
	if err := json.Unmarshal(patchRaw, &patchText); err != nil {
		return "", "", false
	}
	files := parsePatchFileViews(patchText, s.cwd)
	if len(files) == 0 {
		trimmedPatch := strings.TrimSpace(patchText)
		if trimmedPatch == "" {
			return "", "", false
		}
		return "Edited:", "Edited:\n" + trimmedPatch, true
	}

	formatSummaryLine := func(file patchFileView) string {
		line := file.RelPath
		if line == "" {
			line = file.AbsPath
		}
		if file.Added > 0 {
			line += fmt.Sprintf(" +%d", file.Added)
		}
		if file.Removed > 0 {
			line += fmt.Sprintf(" -%d", file.Removed)
		}
		return line
	}
	formatDetailHeader := func(file patchFileView) string {
		line := file.AbsPath
		if line == "" {
			line = file.RelPath
		}
		return line
	}

	if len(files) == 1 {
		single := files[0]
		summaryLine := formatSummaryLine(single)
		detailLines := []string{"Edited: " + formatDetailHeader(single)}
		detailLines = append(detailLines, single.Diff...)
		return "Edited: " + summaryLine, strings.Join(detailLines, "\n"), true
	}

	summaryLines := []string{"Edited:"}
	detailLines := []string{"Edited:"}
	for _, file := range files {
		summaryLines = append(summaryLines, formatSummaryLine(file))

		detailLines = append(detailLines, formatDetailHeader(file))
		detailLines = append(detailLines, file.Diff...)
	}

	return strings.Join(summaryLines, "\n"), strings.Join(detailLines, "\n"), true
}

func parsePatchFileViews(patchText, cwd string) []patchFileView {
	lines := textutil.SplitLinesCRLF(patchText)
	files := make([]patchFileView, 0, 8)
	byAbs := make(map[string]int, 8)

	resolve := func(path string) (string, string) {
		p := strings.TrimSpace(path)
		if p == "" {
			return "", ""
		}
		var abs string
		if filepath.IsAbs(p) {
			abs = filepath.Clean(p)
		} else if cwd != "" {
			abs = filepath.Clean(filepath.Join(cwd, p))
		} else {
			abs = filepath.Clean(p)
		}
		abs = filepath.ToSlash(abs)
		if cwd == "" {
			return abs, "./" + filepath.ToSlash(strings.TrimPrefix(p, "./"))
		}
		rel, err := filepath.Rel(cwd, filepath.FromSlash(abs))
		if err != nil {
			return abs, filepath.ToSlash(p)
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return abs, "./"
		}
		if strings.HasPrefix(rel, "../") || rel == ".." {
			return abs, filepath.ToSlash(p)
		}
		return abs, "./" + rel
	}

	getFile := func(path string) *patchFileView {
		abs, rel := resolve(path)
		if abs == "" {
			return nil
		}
		if idx, ok := byAbs[abs]; ok {
			return &files[idx]
		}
		files = append(files, patchFileView{AbsPath: abs, RelPath: rel, Diff: make([]string, 0, 32)})
		idx := len(files) - 1
		byAbs[abs] = idx
		return &files[idx]
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "*** Add File: "):
			file := getFile(strings.TrimPrefix(line, "*** Add File: "))
			for i+1 < len(lines) && !strings.HasPrefix(lines[i+1], "*** ") {
				i++
				row := lines[i]
				if file == nil {
					continue
				}
				if row == "" {
					file.Diff = append(file.Diff, "")
					continue
				}
				if strings.HasPrefix(row, "+") {
					file.Added++
				}
				file.Diff = append(file.Diff, row)
			}
		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimPrefix(line, "*** Update File: ")
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "*** Move to: ") {
				i++
				path = strings.TrimPrefix(lines[i], "*** Move to: ")
			}
			file := getFile(path)
			for i+1 < len(lines) && !strings.HasPrefix(lines[i+1], "*** ") {
				i++
				row := lines[i]
				if file == nil {
					continue
				}
				if row == "" {
					file.Diff = append(file.Diff, "")
					continue
				}
				switch row[0] {
				case '+':
					file.Added++
				case '-':
					file.Removed++
				}
				file.Diff = append(file.Diff, row)
			}
		case strings.HasPrefix(line, "*** Delete File: "):
			file := getFile(strings.TrimPrefix(line, "*** Delete File: "))
			if file != nil {
				file.Removed++
				file.Diff = append(file.Diff, "-<deleted file>")
			}
		}
	}

	return files
}

func formatToolOutput(raw json.RawMessage) string {
	return toolcodec.FormatOutputForTool("", raw)
}
