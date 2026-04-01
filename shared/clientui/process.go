package clientui

import "time"

type BackgroundProcess struct {
	ID                      string
	OwnerSessionID          string
	OwnerRunID              string
	OwnerStepID             string
	State                   string
	Command                 string
	Workdir                 string
	StartedAt               time.Time
	FinishedAt              time.Time
	ExitCode                *int
	LogPath                 string
	RecentOutput            string
	OutputAvailable         bool
	OutputRetainedFromBytes int64
	OutputRetainedToBytes   int64
	Running                 bool
	StdinOpen               bool
	Backgrounded            bool
	KillRequested           bool
	LastUpdatedAt           time.Time
}

type ProcessClient interface {
	ListProcesses() []BackgroundProcess
	KillProcess(id string) error
	InlineOutput(id string, maxChars int) (string, string, error)
}
