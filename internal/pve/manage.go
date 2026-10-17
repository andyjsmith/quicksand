package pve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UPID identifies an asynchronous Proxmox task.
type UPID string

// CloneOptions configures a clone of a template.
type CloneOptions struct {
	NewID       int
	Name        string
	Description string
	// Full copies the disks; otherwise a linked clone is made.
	Full bool
	// Pool, if set, adds the clone to a resource pool.
	Pool string
}

func (c *Client) Clone(ctx context.Context, node string, vmid int, opts CloneOptions) (UPID, error) {
	form := url.Values{
		"newid": {strconv.Itoa(opts.NewID)},
		"name":  {opts.Name},
		"full":  {boolParam(opts.Full)},
	}
	if opts.Description != "" {
		form.Set("description", opts.Description)
	}
	if opts.Pool != "" {
		form.Set("pool", opts.Pool)
	}
	var upid UPID
	err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "clone"), form, &upid)
	return upid, err
}

// UpdateConfig changes a guest's configuration synchronously. Keys in
// remove are deleted from the config.
func (c *Client) UpdateConfig(ctx context.Context, node string, vmid int, set map[string]string, remove []string) error {
	form := url.Values{}
	for k, v := range set {
		form.Set(k, v)
	}
	if len(remove) > 0 {
		form.Set("delete", strings.Join(remove, ","))
	}
	return c.do(ctx, http.MethodPut, vmPath(node, vmid, "config"), form, nil)
}

// PowerAction is a status change Proxmox can apply to a guest.
type PowerAction string

const (
	PowerStart    PowerAction = "start"
	PowerShutdown PowerAction = "shutdown"
	PowerStop     PowerAction = "stop"
	PowerReboot   PowerAction = "reboot"
	PowerReset    PowerAction = "reset"
	PowerSuspend  PowerAction = "suspend"
	PowerResume   PowerAction = "resume"
	// PowerHibernate saves the guest's memory to disk and stops it;
	// PowerStart resumes it.
	PowerHibernate PowerAction = "hibernate"
)

func (c *Client) Power(ctx context.Context, node string, vmid int, action PowerAction) (UPID, error) {
	path, form := vmPath(node, vmid, "status", string(action)), url.Values{}
	if action == PowerHibernate {
		path, form = vmPath(node, vmid, "status", "suspend"), url.Values{"todisk": {"1"}}
	}
	var upid UPID
	err := c.do(ctx, http.MethodPost, path, form, &upid)
	return upid, err
}

// Destroy deletes a guest along with its disks.
func (c *Client) Destroy(ctx context.Context, node string, vmid int) (UPID, error) {
	var upid UPID
	err := c.do(ctx, http.MethodDelete, vmPath(node, vmid), url.Values{
		"purge":                      {"1"},
		"destroy-unreferenced-disks": {"1"},
	}, &upid)
	return upid, err
}

// Status is a guest's current runtime state.
type Status struct {
	// Status is "running" or "stopped".
	Status string `json:"status"`
	// QMPStatus refines Status, e.g. "paused" or "suspended".
	QMPStatus string  `json:"qmpstatus"`
	Lock      string  `json:"lock"`
	Uptime    int64   `json:"uptime"`
	CPU       float64 `json:"cpu"`
	Mem       int64   `json:"mem"`
	MaxMem    int64   `json:"maxmem"`
	// Agent is 1 when the guest agent is enabled in the config.
	Agent int `json:"agent"`
}

// Hibernated reports whether the guest is stopped with its memory saved to
// disk, to resume from when it's started.
func (s Status) Hibernated() bool { return s.Status == "stopped" && s.Lock == "suspended" }

// PowerState collapses Status into running, paused, suspended, hibernated
// or stopped.
func (s Status) PowerState() string {
	if s.Hibernated() {
		return "hibernated"
	}
	if s.Status == "running" && (s.QMPStatus == "paused" || s.QMPStatus == "suspended" || s.QMPStatus == "prelaunch") {
		if s.QMPStatus == "prelaunch" {
			return "paused"
		}
		return s.QMPStatus
	}
	return s.Status
}

func (c *Client) Status(ctx context.Context, node string, vmid int) (Status, error) {
	var s Status
	err := c.do(ctx, http.MethodGet, vmPath(node, vmid, "status", "current"), nil, &s)
	return s, err
}

// AgentPing reports whether the guest agent answers.
func (c *Client) AgentPing(ctx context.Context, node string, vmid int) error {
	return c.do(ctx, http.MethodPost, vmPath(node, vmid, "agent", "ping"), url.Values{}, nil)
}

// TaskStatus is the state of an asynchronous task.
type TaskStatus struct {
	// Status is "running" or "stopped".
	Status string `json:"status"`
	// ExitStatus is "OK" for success, otherwise an error message.
	ExitStatus string `json:"exitstatus"`
}

func (c *Client) TaskStatus(ctx context.Context, node string, upid UPID) (TaskStatus, error) {
	var t TaskStatus
	err := c.do(ctx, http.MethodGet, taskPath(node, upid, "status"), nil, &t)
	return t, err
}

// TaskLog returns task log lines from line start onwards.
func (c *Client) TaskLog(ctx context.Context, node string, upid UPID, start int) ([]string, error) {
	var lines []struct {
		N int    `json:"n"`
		T string `json:"t"`
	}
	err := c.do(ctx, http.MethodGet, taskPath(node, upid, "log"), url.Values{
		"start": {strconv.Itoa(start)},
		"limit": {"500"},
	}, &lines)
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.T
	}
	return out, err
}

// WaitTask polls a task until it finishes, calling onLog with new log lines.
// It returns an error if the task fails.
func (c *Client) WaitTask(ctx context.Context, node string, upid UPID, onLog func(line string)) error {
	logged := 0
	for {
		status, err := c.TaskStatus(ctx, node, upid)
		if err != nil {
			return err
		}
		if onLog != nil {
			// The log API counts lines from 0; the first line is the task header.
			if lines, err := c.TaskLog(ctx, node, upid, logged); err == nil {
				for _, l := range lines {
					onLog(l)
				}
				logged += len(lines)
			}
		}
		if status.Status == "stopped" {
			if status.ExitStatus != "OK" {
				return &TaskError{Message: status.ExitStatus}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// TaskError is a Proxmox task that finished unsuccessfully.
type TaskError struct{ Message string }

func (e *TaskError) Error() string { return "proxmox task failed: " + e.Message }

// IsNotFound reports whether err means the guest does not exist.
func IsNotFound(err error) bool {
	var pe *Error
	return errors.As(err, &pe) && (pe.Status == http.StatusNotFound ||
		pe.Status == http.StatusInternalServerError && strings.Contains(pe.Message, "does not exist"))
}

// SetNetOption sets one option (e.g. firewall=0) in a netX property,
// replacing any existing value.
func SetNetOption(net, key, value string) string {
	parts := []string{}
	for p := range strings.SplitSeq(net, ",") {
		if p != "" && !strings.HasPrefix(p, key+"=") {
			parts = append(parts, p)
		}
	}
	return strings.Join(append(parts, key+"="+value), ",")
}

// SetNetBridge rewrites a netX property (e.g. "virtio=AA:BB:..,bridge=vmbr0,firewall=1")
// to use another bridge and VLAN tag (0 for none), keeping the model, MAC
// address and other options.
func SetNetBridge(net string, bridge string, vlan int) string {
	parts := []string{}
	for p := range strings.SplitSeq(net, ",") {
		if p == "" || strings.HasPrefix(p, "bridge=") || strings.HasPrefix(p, "tag=") {
			continue
		}
		parts = append(parts, p)
	}
	parts = append(parts, "bridge="+bridge)
	if vlan > 0 {
		parts = append(parts, "tag="+strconv.Itoa(vlan))
	}
	return strings.Join(parts, ",")
}

func taskPath(node string, upid UPID, rest string) string {
	// Path segments are escaped by the client when the URL is built.
	return fmt.Sprintf("/nodes/%s/tasks/%s/%s", node, upid, rest)
}

func boolParam(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
