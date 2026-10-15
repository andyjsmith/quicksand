package pve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// Snapshot is one of a guest's snapshots.
type Snapshot struct {
	Name        string
	Description string
	// Taken is when it was taken (zero for the "current" pseudo-entry).
	Taken time.Time
	// Memory says it includes the guest's RAM, so rolling back resumes it.
	Memory bool
}

func (s *Snapshot) UnmarshalJSON(b []byte) error {
	var raw struct {
		Name        string      `json:"name"`
		Description string      `json:"description"`
		SnapTime    int64       `json:"snaptime"`
		VMState     json.Number `json:"vmstate"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*s = Snapshot{Name: raw.Name, Description: raw.Description, Memory: raw.VMState != "" && raw.VMState != "0"}
	if raw.SnapTime > 0 {
		s.Taken = time.Unix(raw.SnapTime, 0).UTC()
	}
	return nil
}

// Snapshots lists a guest's snapshots.
func (c *Client) Snapshots(ctx context.Context, node string, vmid int) ([]Snapshot, error) {
	var out []Snapshot
	err := c.do(ctx, http.MethodGet, vmPath(node, vmid, "snapshot"), nil, &out)
	return out, err
}

// CreateSnapshot snapshots a guest's disks, and its RAM if memory is set.
func (c *Client) CreateSnapshot(ctx context.Context, node string, vmid int, name, description string, memory bool) (UPID, error) {
	var upid UPID
	err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "snapshot"), url.Values{
		"snapname": {name}, "description": {description}, "vmstate": {boolParam(memory)},
	}, &upid)
	return upid, err
}

// RollbackSnapshot returns a guest to a snapshot, keeping the snapshot.
func (c *Client) RollbackSnapshot(ctx context.Context, node string, vmid int, name string) (UPID, error) {
	var upid UPID
	err := c.do(ctx, http.MethodPost, vmPath(node, vmid, "snapshot", name, "rollback"), url.Values{}, &upid)
	return upid, err
}

// DeleteSnapshot removes a snapshot.
func (c *Client) DeleteSnapshot(ctx context.Context, node string, vmid int, name string) (UPID, error) {
	var upid UPID
	err := c.do(ctx, http.MethodDelete, vmPath(node, vmid, "snapshot", name), nil, &upid)
	return upid, err
}
