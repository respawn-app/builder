package session

import (
	"encoding/json"
	"time"
)

type LockedContract struct {
	Model             string                     `json:"model"`
	Temperature       float64                    `json:"temperature"`
	MaxOutputToken    int                        `json:"max_output_token"`
	EnabledTools      []string                   `json:"enabled_tools,omitempty"`
	ToolPreambles     *bool                      `json:"tool_preambles,omitempty"`
	ModelCapabilities LockedModelCapabilities    `json:"model_capabilities,omitempty"`
	ProviderContract  LockedProviderCapabilities `json:"provider_contract,omitempty"`
	LockedAt          time.Time                  `json:"locked_at"`
}

type LockedModelCapabilities struct {
	SupportsReasoningEffort bool `json:"supports_reasoning_effort,omitempty"`
	SupportsVisionInputs    bool `json:"supports_vision_inputs,omitempty"`
}

type LockedProviderCapabilities struct {
	ProviderID                    string `json:"provider_id,omitempty"`
	SupportsResponsesAPI          bool   `json:"supports_responses_api,omitempty"`
	SupportsResponsesCompact      bool   `json:"supports_responses_compact,omitempty"`
	SupportsNativeWebSearch       bool   `json:"supports_native_web_search,omitempty"`
	SupportsReasoningEncrypted    bool   `json:"supports_reasoning_encrypted,omitempty"`
	SupportsServerSideContextEdit bool   `json:"supports_server_side_context_edit,omitempty"`
	IsOpenAIFirstParty            bool   `json:"is_openai_first_party,omitempty"`
}

type ContinuationContext struct {
	OpenAIBaseURL string `json:"openai_base_url,omitempty"`
}

type Meta struct {
	SessionID          string               `json:"session_id"`
	Name               string               `json:"name,omitempty"`
	FirstPromptPreview string               `json:"first_prompt_preview,omitempty"`
	InputDraft         string               `json:"input_draft,omitempty"`
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
	SessionID          string    `json:"session_id"`
	Name               string    `json:"name,omitempty"`
	FirstPromptPreview string    `json:"first_prompt_preview,omitempty"`
	UpdatedAt          time.Time `json:"updated_at"`
	Path               string    `json:"path"`
}
