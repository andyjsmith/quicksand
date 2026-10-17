package agentproto

import "time"

// DefaultPort is the agent's TCP port unless configured otherwise.
const DefaultPort = 7070

// Version is the agent protocol version, reported by /v1/info.
const Version = "3"

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
	// Launch is the app the agent keeps running in the guest's desktop
	// session, if it was given one (see LaunchRequest).
	Launch *LaunchState `json:"launch,omitempty"`
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

// LinkRequest is sent with PUT /v1/links: the addresses of the guest's
// link-network NICs (networks the user joined VMs into), each found by its
// MAC. It's the complete set; the agent keeps those addresses on their
// NICs (no gateway) until the next request, and the server repeats it
// after the agent restarts.
type LinkRequest struct {
	Links []Link `json:"links"`
}

type Link struct {
	MAC string `json:"mac"`
	// Address is the NIC's address with its prefix length, e.g. "10.98.0.5/16".
	Address string `json:"address"`
}

// LinkResult reports, per link, whether its address is in place. A NIC
// that was just added may take a moment to appear in the guest; the agent
// keeps trying.
type LinkResult struct {
	Links []LinkStatus `json:"links"`
}

type LinkStatus struct {
	MAC        string `json:"mac"`
	Interface  string `json:"interface,omitempty"`
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"`
}

// Clipboard is the guest clipboard's text, returned by GET /v1/clipboard.
// Seq changes whenever the text does, so a client can wait for the next
// change with ?after=SEQ&wait=SECONDS (a long poll).
type Clipboard struct {
	Seq  uint64 `json:"seq"`
	Text string `json:"text"`
}

// ClipboardSet is sent with PUT /v1/clipboard to put text on the guest
// clipboard (when the user pastes into the console).
type ClipboardSet struct {
	Text string `json:"text"`
}

// MaxClipboardText caps clipboard text in either direction.
const MaxClipboardText = 1 << 20

// MaxClipboardWait caps how long GET /v1/clipboard waits for a change.
const MaxClipboardWait = 25 * time.Second

// URLPlaceholder stands for the URL in LaunchSettings.OpenURL.
const URLPlaceholder = "{url}"

// LaunchSettings describe an app the agent keeps running in the guest's
// desktop session, as the signed-in user. The agent saves them, so they
// survive guest reboots.
type LaunchSettings struct {
	// Command is the program and its arguments; empty launches nothing.
	Command []string `json:"command,omitempty"`
	// OpenURL, if set, opens a URL in the app: the same, with URLPlaceholder
	// where the URL goes. It's run as well as Command when the app is
	// running (browsers hand the URL to the running instance), and instead
	// of it when the app isn't, so the app starts on the URL.
	OpenURL []string `json:"openURL,omitempty"`
	// Restart starts the app again when it exits. Otherwise it's started
	// once per desktop session.
	Restart bool `json:"restart,omitempty"`
}

// LaunchRequest is sent with PUT /v1/launch. Its settings replace the
// agent's; the app is restarted only if they changed.
type LaunchRequest struct {
	LaunchSettings
	// Open lists URLs to open in the app (which needs OpenURL), now or as
	// soon as the desktop session allows.
	Open []string `json:"open,omitempty"`
	// Start starts the app if it isn't running, even if it was started in
	// this session before.
	Start bool `json:"start,omitempty"`
}

// LaunchState is what the agent is doing about its LaunchSettings,
// returned by PUT /v1/launch and in Info.
type LaunchState struct {
	LaunchSettings
	Running bool `json:"running"`
	// Error says why the app isn't running, e.g. that nobody is signed in
	// to the guest's desktop or the app failed to start.
	Error string `json:"error,omitempty"`
}
