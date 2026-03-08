package session

import (
	"encoding/json"
	"time"
)

type LockedContract struct {
	Model          string    `json:"model"`
	Temperature    float64   `json:"temperature"`
	MaxOutputToken int       `json:"max_output_token"`
	EnabledTools   []string  `json:"enabled_tools,omitempty"`
	ToolPreambles  *bool     `json:"tool_preambles,omitempty"`
	LockedAt       time.Time `json:"locked_at"`
}

type ContinuationContext struct {
	OpenAIBaseURL string `json:"openai_base_url,omitempty"`
}

type Meta struct {
	SessionID          string               `json:"session_id"`
	Name               string               `json:"name,omitempty"`
	ParentSessionID    string               `json:"parent_session_id,omitempty"`
	WorkspaceRoot      string               `json:"workspace_root"`
	WorkspaceContainer string               `json:"workspace_container"`
	Continuation       *ContinuationContext `json:"continuation,omitempty"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	LastSequence       int64                `json:"last_sequence"`
	ModelRequestCount  int64                `json:"model_request_count"`
	InFlightStep       bool                 `json:"in_flight_step"`
	AgentsInjected     bool                 `json:"agents_injected"`
	Locked             *LockedContract      `json:"locked,omitempty"`
}

type Event struct {
	Seq       int64           `json:"seq"`
	Timestamp time.Time       `json:"timestamp"`
	Kind      string          `json:"kind"`
	StepID    string          `json:"step_id,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

type Summary struct {
	SessionID string    `json:"session_id"`
	Name      string    `json:"name,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	Path      string    `json:"path"`
}
