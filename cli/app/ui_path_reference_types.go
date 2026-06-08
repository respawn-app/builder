package app

import "time"

const (
	uiPathReferenceLoadingDelay = 500 * time.Millisecond
	uiPathReferenceBuildTimeout = 2 * time.Minute
)

type uiPickerKind uint8

const (
	uiPickerKindNone uiPickerKind = iota
	uiPickerKindSlashCommand
	uiPickerKindPathReference
)

type uiPickerPresentation struct {
	kind      uiPickerKind
	visible   bool
	rows      []uiPickerRow
	selection int
	start     int
	lineCount int
}

type uiPickerRow struct {
	primary     string
	secondary   string
	boldPrimary bool
	selectable  bool
	muted       bool
}

type uiPathReferenceCandidate struct {
	Path      string
	Directory bool
}

type uiPathReferenceQuery struct {
	Active          bool
	Start           int
	End             int
	RawQuery        string
	NormalizedQuery string
}

type uiPathReferenceState struct {
	tracked          uiPathReferenceQuery
	matches          []uiPathReferenceCandidate
	selection        int
	queryToken       uint64
	draftToken       uint64
	workspaceRoot    string
	normalizedQuery  string
	pending          bool
	loading          bool
	corpusGeneration uint64
}

type uiPathReferenceSearchEvent = any

type uiPathReferenceSearchRequestMessage = any

type uiPathReferenceSearchRequest struct {
	WorkspaceRoot   string
	DraftToken      uint64
	QueryToken      uint64
	NormalizedQuery string
}

type uiPathReferenceCorpusReadyMsg struct {
	WorkspaceRoot    string
	CorpusGeneration uint64
}

type uiPathReferenceCorpusFailedMsg struct {
	WorkspaceRoot    string
	CorpusGeneration uint64
	Err              error
}

type uiPathReferenceMatchResultMsg struct {
	WorkspaceRoot    string
	CorpusGeneration uint64
	DraftToken       uint64
	QueryToken       uint64
	NormalizedQuery  string
	Matches          []uiPathReferenceCandidate
}

type uiPathReferenceLoadingDelayMsg struct {
	WorkspaceRoot    string
	CorpusGeneration uint64
	DraftToken       uint64
	QueryToken       uint64
	NormalizedQuery  string
}

type uiPathReferencePrewarmRequest struct {
	workspaceRoot string
}
