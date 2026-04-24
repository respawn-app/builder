package patchformat

import "strings"

type Document struct {
	Hunks []any
}

type AddFile struct {
	Path    string
	Content []string
}

type DeleteFile struct {
	Path string
}

type UpdateFile struct {
	Path    string
	MoveTo  string
	Changes []ChangeLine
}

type ChangeLine struct {
	Kind      rune
	Content   string
	EndOfFile bool
}

type RenderedLineKind string

const (
	RenderedLineKindHeader RenderedLineKind = "header"
	RenderedLineKindFile   RenderedLineKind = "file"
	RenderedLineKindDiff   RenderedLineKind = "diff"
	RenderedLineKindRaw    RenderedLineKind = "raw"
)

type RenderedLine struct {
	Kind      RenderedLineKind
	Text      string
	FileIndex int
	Path      string
}

type RenderedFile struct {
	AbsPath string
	RelPath string
	Added   int
	Removed int
	Diff    []string
}

type RenderedPatch struct {
	Files        []RenderedFile
	SummaryLines []RenderedLine
	DetailLines  []RenderedLine
}

func (r RenderedPatch) SummaryText() string {
	return joinRenderedLines(r.SummaryLines)
}

func (r RenderedPatch) DetailText() string {
	return joinRenderedLines(r.DetailLines)
}

func joinRenderedLines(lines []RenderedLine) string {
	if len(lines) == 0 {
		return ""
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, line.Text)
	}
	return strings.Join(out, "\n")
}
