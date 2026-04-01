package protocol

import "time"

const (
	Version           = "1"
	RPCPath           = "/rpc"
	HealthPath        = "/healthz"
	ReadinessPath     = "/readyz"
	DiscoveryFilename = "app-server.json"
)

type CapabilityFlags struct {
	JSONRPCWebSocket  bool `json:"jsonrpc_websocket"`
	ProjectAttach     bool `json:"project_attach"`
	SessionAttach     bool `json:"session_attach"`
	HealthEndpoint    bool `json:"health_endpoint"`
	ReadinessEndpoint bool `json:"readiness_endpoint"`
	RunPrompt         bool `json:"run_prompt"`
	SessionActivity   bool `json:"session_activity"`
	ProcessOutput     bool `json:"process_output"`
}

type ServerIdentity struct {
	ProtocolVersion string          `json:"protocol_version"`
	ServerID        string          `json:"server_id"`
	ProjectID       string          `json:"project_id"`
	WorkspaceRoot   string          `json:"workspace_root"`
	PID             int             `json:"pid"`
	Capabilities    CapabilityFlags `json:"capabilities"`
}

type DiscoveryRecord struct {
	Identity  ServerIdentity `json:"identity"`
	HTTPURL   string         `json:"http_url"`
	RPCURL    string         `json:"rpc_url"`
	HealthURL string         `json:"health_url"`
	ReadyURL  string         `json:"ready_url"`
	StartedAt time.Time      `json:"started_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}
