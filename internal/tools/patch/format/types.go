package format

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
	Kind    rune
	Content string
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
