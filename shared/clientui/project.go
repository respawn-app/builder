package clientui

import "time"

type ProjectAvailability string

const (
	ProjectAvailabilityAvailable    ProjectAvailability = "available"
	ProjectAvailabilityMissing      ProjectAvailability = "missing"
	ProjectAvailabilityInaccessible ProjectAvailability = "inaccessible"
)

type ProjectSummary struct {
	ProjectID    string
	DisplayName  string
	RootPath     string
	Availability ProjectAvailability
	SessionCount int
	UpdatedAt    time.Time
}

type ProjectOverview struct {
	Project  ProjectSummary
	Sessions []SessionSummary
}

type SessionSummary struct {
	SessionID          string
	Name               string
	FirstPromptPreview string
	UpdatedAt          time.Time
}
