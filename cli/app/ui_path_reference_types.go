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

type uiPathReferenceSearchEvent interface {
	pathReferenceSearchEvent()
}

type uiPathReferenceSearchRequestMessage interface {
	pathReferenceSearchRequestMessage()
}

type uiPathReferenceSearchRequest struct {
	WorkspaceRoot   string
	DraftToken      uint64
	QueryToken      uint64
	NormalizedQuery string
}

func (uiPathReferenceSearchRequest) pathReferenceSearchRequestMessage() {}

type uiPathReferenceCorpusReadyMsg struct {
	WorkspaceRoot    string
	CorpusGeneration uint64
}

func (uiPathReferenceCorpusReadyMsg) pathReferenceSearchEvent() {}

type uiPathReferenceCorpusFailedMsg struct {
	WorkspaceRoot    string
	CorpusGeneration uint64
	Err              error
}

func (uiPathReferenceCorpusFailedMsg) pathReferenceSearchEvent() {}

type uiPathReferenceMatchResultMsg struct {
	WorkspaceRoot    string
	CorpusGeneration uint64
	DraftToken       uint64
	QueryToken       uint64
	NormalizedQuery  string
	Matches          []uiPathReferenceCandidate
}

func (uiPathReferenceMatchResultMsg) pathReferenceSearchEvent() {}

type uiPathReferenceLoadingDelayMsg struct {
	WorkspaceRoot    string
	CorpusGeneration uint64
	DraftToken       uint64
	QueryToken       uint64
	NormalizedQuery  string
}

func (uiPathReferenceLoadingDelayMsg) pathReferenceSearchEvent() {}

type uiPathReferencePrewarmRequest struct {
	workspaceRoot string
}

func (uiPathReferencePrewarmRequest) pathReferenceSearchRequestMessage() {}
