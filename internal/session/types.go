package session

import (
	"encoding/json"
	"time"
)

type LockedContract struct {
	Model          string          `json:"model"`
	Temperature    float64         `json:"temperature"`
	MaxOutputToken int             `json:"max_output_token"`
	ToolsJSON      json.RawMessage `json:"tools_json"`
	SystemPrompt   string          `json:"system_prompt"`
	LockedAt       time.Time       `json:"locked_at"`
}

type Meta struct {
	SessionID          string          `json:"session_id"`
	WorkspaceRoot      string          `json:"workspace_root"`
	WorkspaceContainer string          `json:"workspace_container"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	LastSequence       int64           `json:"last_sequence"`
	ModelRequestCount  int64           `json:"model_request_count"`
	InFlightStep       bool            `json:"in_flight_step"`
	AgentsInjected     bool            `json:"agents_injected"`
	Locked             *LockedContract `json:"locked,omitempty"`
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
	UpdatedAt time.Time `json:"updated_at"`
	Path      string    `json:"path"`
}
