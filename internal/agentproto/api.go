package agentproto

import "time"

// DefaultPort is the agent's TCP port unless configured otherwise.
const DefaultPort = 7070

// Version is the agent protocol version, reported by /v1/info.
const Version = "2"

// Info is returned by GET /v1/info.
type Info struct {
	ProtocolVersion string `json:"protocolVersion"`
	AgentVersion    string `json:"agentVersion"`
	Hostname        string `json:"hostname"`
	OS              string `json:"os"`
	Arch            string `json:"arch"`
	User            string `json:"user"`
	// FilesDir is where file browsing starts unless the server says otherwise
	// (the template's filesDir): the filesystem root, or the system drive.
	FilesDir string `json:"filesDir"`
	// PathSeparator is the guest's path separator, "/" or "\\".
	PathSeparator string      `json:"pathSeparator"`
	Time          time.Time   `json:"time"`
	Interfaces    []Interface `json:"interfaces"`
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

// ProcessList is returned by GET /v1/processes.
type ProcessList struct {
	Processes []Process `json:"processes"`
	// Time the list was taken, and the guest's CPU count and memory, for
	// turning CPU times into usage between two lists.
	Time        time.Time `json:"time"`
	NumCPU      int       `json:"numCPU"`
	MemoryTotal uint64    `json:"memoryTotal"`
}

type Process struct {
	PID  int    `json:"pid"`
	PPID int    `json:"ppid"`
	Name string `json:"name"`
	// User owning the process; empty if it can't be read.
	User string `json:"user,omitempty"`
	// Command is the command line (Linux) or executable path (Windows).
	Command string `json:"command,omitempty"`
	// Memory is the resident set (working set on Windows) in bytes.
	Memory uint64 `json:"memory"`
	// CPUTimeMs is user plus system CPU time used so far.
	CPUTimeMs int64     `json:"cpuTimeMs"`
	Threads   int       `json:"threads,omitempty"`
	Started   time.Time `json:"started,omitzero"`
	// Services are the Windows services the process hosts (several for a
	// shared svchost.exe), or its systemd service unit on Linux.
	Services []ProcessService `json:"services,omitempty"`
}

type ProcessService struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

// KillRequest is the body of POST /v1/processes/{pid}/kill. Force kills
// immediately; otherwise Linux processes get SIGTERM. Windows processes
// are always terminated.
type KillRequest struct {
	Force bool `json:"force"`
}

// File paths in the files API are absolute guest paths, or relative to a
// base directory (the "base" query parameter, defaulting to the agent's
// Info.FilesDir). On Windows the path "/" lists the drives.

// FileList is returned by GET /v1/files.
type FileList struct {
	// Path is the listed directory, absolute and cleaned.
	Path string `json:"path"`
	// Parent is the directory above Path, empty at the top.
	Parent  string      `json:"parent"`
	Entries []FileEntry `json:"entries"`
}

type FileEntry struct {
	Name string `json:"name"`
	// Path is the entry's absolute path.
	Path    string    `json:"path"`
	Dir     bool      `json:"dir"`
	Symlink bool      `json:"symlink,omitempty"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime,omitzero"`
	Mode    string    `json:"mode,omitempty"`
}

// ShellRequest starts a command over GET /v1/shell (a stream, see
// AcceptStream; JSON in the "request" query parameter). The stream then
// carries frames (see frames.go).
type ShellRequest struct {
	// Command runs through the guest's shell (sh -c, cmd /c); empty starts
	// an interactive shell (a login shell; on Windows PowerShell with a
	// terminal, cmd.exe without).
	Command string `json:"command,omitempty"`
	// Shell is the shell program to use instead of the default: a name
	// looked up in PATH or a path, e.g. "cmd", "pwsh" or "zsh". Commands
	// run through it with -c, or /c and -Command for cmd and PowerShell.
	Shell string `json:"shell,omitempty"`
	// PTY gives the command a terminal of Cols×Rows; otherwise it gets
	// pipes and stdout and stderr are kept apart.
	PTY  bool   `json:"pty,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
	Term string `json:"term,omitempty"`
	// Dir is the working directory; empty means Info.FilesDir.
	Dir string `json:"dir,omitempty"`
}

// ShellExit is the payload of the final FrameExit.
type ShellExit struct {
	// Code is -1 if the command couldn't run or was killed.
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"`
}

// Resize is the payload of FrameResize.
type Resize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// ConnectionList is returned by GET /v1/connections.
type ConnectionList struct {
	Connections []Connection `json:"connections"`
}

// Connection is a TCP or UDP socket, like a netstat line.
type Connection struct {
	// Proto is tcp, tcp6, udp or udp6.
	Proto  string `json:"proto"`
	Local  string `json:"local"`
	Remote string `json:"remote,omitempty"`
	// State is the TCP state (LISTEN, ESTABLISHED, ...); empty for UDP.
	State string `json:"state,omitempty"`
	// PID owning the socket, 0 if unknown.
	PID         int    `json:"pid,omitempty"`
	ProcessName string `json:"processName,omitempty"`
}

// ZipRequest is the body of POST /v1/files/zip, which streams a file or
// directory as a zip archive encrypted with ZipCrypto (weak, but readable
// by every unzip tool, Windows Explorer included).
type ZipRequest struct {
	Password string `json:"password"`
}

// DisplayRequest is the body of POST /v1/display, which changes the guest's
// screen resolution (Windows guests): exactly with QXL and VirtIO GPU
// drivers that take custom sizes, else to the closest mode on offer.
type DisplayRequest struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// DisplayResult is the resolution set, which may be adjusted from the one
// requested (e.g. to a multiple of 8 wide).
type DisplayResult struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	// Adapter is the display driver used: "virtio-gpu", "qxl", or another
	// driver's name.
	Adapter string `json:"adapter"`
	// Exact is false when the driver can't take custom sizes and the
	// closest mode it offers was used instead.
	Exact bool `json:"exact"`
}

// FileHashes is returned by GET /v1/files/hash.
type FileHashes struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	MD5    string `json:"md5"`
	SHA1   string `json:"sha1"`
	SHA256 string `json:"sha256"`
}
