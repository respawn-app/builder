package patch

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
