package serverapi

import (
	"context"
	"errors"
	"strings"

	"builder/shared/clientui"
)

type SessionMainViewRequest struct {
	SessionID string
}

type SessionMainViewResponse struct {
	MainView clientui.RuntimeMainView
}

type SessionTranscriptPageRequest struct {
	SessionID                string                    `json:"session_id"`
	Offset                   int                       `json:"offset,omitempty"`
	Limit                    int                       `json:"limit,omitempty"`
	Page                     int                       `json:"page,omitempty"`
	PageSize                 int                       `json:"page_size,omitempty"`
	Window                   clientui.TranscriptWindow `json:"window,omitempty"`
	KnownRevision            int64                     `json:"known_revision,omitempty"`
	KnownCommittedEntryCount int                       `json:"known_committed_entry_count,omitempty"`
}

type SessionTranscriptPageResponse struct {
	Transcript clientui.TranscriptPage `json:"transcript"`
}

type RunGetRequest struct {
	SessionID string
	RunID     string
}

type RunGetResponse struct {
	Run *clientui.RunView
}

type SessionViewService interface {
	GetSessionMainView(ctx context.Context, req SessionMainViewRequest) (SessionMainViewResponse, error)
	GetSessionTranscriptPage(ctx context.Context, req SessionTranscriptPageRequest) (SessionTranscriptPageResponse, error)
	GetRun(ctx context.Context, req RunGetRequest) (RunGetResponse, error)
}

func (r SessionMainViewRequest) Validate() error {
	return validateRequiredSessionID(r.SessionID)
}

func (r RunGetRequest) Validate() error {
	if err := validateRequiredSessionID(r.SessionID); err != nil {
		return err
	}
	if strings.TrimSpace(r.RunID) == "" {
		return errors.New("run_id is required")
	}
	return nil
}

func (r SessionTranscriptPageRequest) Validate() error {
	if err := validateRequiredSessionID(r.SessionID); err != nil {
		return err
	}
	if r.Offset < 0 {
		return errors.New("offset must be >= 0")
	}
	if r.Limit < 0 {
		return errors.New("limit must be >= 0")
	}
	if r.Page < 0 {
		return errors.New("page must be >= 0")
	}
	if r.PageSize < 0 {
		return errors.New("page_size must be >= 0")
	}
	if r.KnownCommittedEntryCount < 0 {
		return errors.New("known_committed_entry_count must be >= 0")
	}
	return nil
}
