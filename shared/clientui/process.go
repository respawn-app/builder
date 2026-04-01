package clientui

import "time"

type BackgroundProcess struct {
	ID             string
	OwnerSessionID string
	OwnerRunID     string
	OwnerStepID    string
	State          string
	Command        string
	Workdir        string
	StartedAt      time.Time
	FinishedAt     time.Time
	ExitCode       *int
	LogPath        string
	RecentOutput   string
	Running        bool
	StdinOpen      bool
	Backgrounded   bool
	KillRequested  bool
	LastUpdatedAt  time.Time
}

type ProcessClient interface {
	ListProcesses() []BackgroundProcess
}
