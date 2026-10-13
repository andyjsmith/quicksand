package agentproto

import "time"

// DefaultPort is the agent's TCP port unless configured otherwise.
const DefaultPort = 7070

// Version is the agent protocol version, reported by /v1/info.
const Version = "1"

// Info is returned by GET /v1/info.
type Info struct {
	ProtocolVersion string      `json:"protocolVersion"`
	AgentVersion    string      `json:"agentVersion"`
	Hostname        string      `json:"hostname"`
	OS              string      `json:"os"`
	Arch            string      `json:"arch"`
	User            string      `json:"user"`
	Time            time.Time   `json:"time"`
	Interfaces      []Interface `json:"interfaces"`
}

type Interface struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Addresses []string `json:"addresses"`
}

// ExecRequest is the body of POST /v1/exec. Exactly one of Argv or Shell is
// set: Argv runs a program directly, Shell runs a command line through the
// guest's shell (sh -c, or cmd /c on Windows).
type ExecRequest struct {
	Argv           []string `json:"argv,omitempty"`
	Shell          string   `json:"shell,omitempty"`
	Stdin          []byte   `json:"stdin,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
}

// ExecResponse is the result of a finished command.
type ExecResponse struct {
	// ExitCode is -1 when the command couldn't be started or was killed.
	ExitCode int    `json:"exitCode"`
	Stdout   []byte `json:"stdout"`
	Stderr   []byte `json:"stderr"`
	// Truncated is set when output exceeded MaxExecOutput.
	Truncated  bool   `json:"truncated,omitempty"`
	TimedOut   bool   `json:"timedOut,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

const (
	DefaultExecTimeout = 60 * time.Second
	MaxExecTimeout     = 30 * time.Minute
	// MaxExecOutput caps stdout and stderr each.
	MaxExecOutput = 4 << 20
)

// Error is the body of a non-2xx response.
type Error struct {
	Error string `json:"error"`
}
