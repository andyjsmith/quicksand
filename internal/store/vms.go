package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// VMState is where a deployed VM is in its lifecycle. Power state (running,
// stopped, ...) lives in Proxmox and is not stored.
type VMState string

const (
	VMDeploying  VMState = "deploying"
	VMReady      VMState = "ready"
	VMFailed     VMState = "failed"
	VMDestroying VMState = "destroying"
)

var (
	// ErrNoVMID is returned when every VM ID in the configured pool is in use.
	ErrNoVMID = errors.New("no free VM IDs")
	// ErrNoAgentIP is returned when the agent subnet has no free addresses.
	ErrNoAgentIP = errors.New("no free agent addresses")
)

type VM struct {
	ID         string
	OwnerID    int64
	Name       string
	TemplateID string
	Node       string
	VMID       int
	Cores      int
	MemoryMiB  int
	NetworkID  string
	State      VMState
	// Step and Progress (0..1) describe a deployment in progress.
	Step      string
	Progress  float64
	Error     string
	CreatedAt time.Time
	// AgentIP is the VM's address on the agent network, if it has an agent.
	AgentIP string
	// ExpiresAt is when the VM is destroyed, zero if never; Extends counts
	// how many times its owner pushed that back.
	ExpiresAt time.Time
	Extends   int
	// LinkID is the link network the VM is in, 0 for none. LinkIP is its
	// address on link networks, kept from when it was first linked.
	LinkID int64
	LinkIP string
}

const vmColumns = `id, owner_id, name, template_id, node, vmid, cores, memory_mib, network_id,
	state, step, progress, error, created_at, agent_ip, expires_at, extends, link_id, link_ip`

func scanVM(row interface{ Scan(...any) error }) (VM, error) {
	var v VM
	var created, expires int64
	var link sql.NullInt64
	err := row.Scan(&v.ID, &v.OwnerID, &v.Name, &v.TemplateID, &v.Node, &v.VMID, &v.Cores, &v.MemoryMiB,
		&v.NetworkID, &v.State, &v.Step, &v.Progress, &v.Error, &created, &v.AgentIP, &expires, &v.Extends,
		&link, &v.LinkIP)
	v.LinkID = link.Int64
	if errors.Is(err, sql.ErrNoRows) {
		return VM{}, ErrNotFound
	}
	v.CreatedAt = fromUnix(created)
	if expires != 0 {
		v.ExpiresAt = fromUnix(expires)
	}
	return v, err
}

// Limit caps how many deployments may exist with TemplateID ("" for any)
// and OwnerID (0 for anyone's); CreateVM fails with ErrLimit (wrapped with
// Message) when creating one would exceed Max.
type Limit struct {
	TemplateID string
	OwnerID    int64
	Max        int
	Message    string
}

// ErrLimit is returned when a deployment would exceed a Limit.
var ErrLimit = errors.New("quota reached")

// CreateVM records a new deployment, assigning it the lowest free VM ID in
// [fromID, toID] that is not in use by another deployment or in exclude
// (IDs Proxmox already has). It checks limits in the same transaction.
func (s *Store) CreateVM(ctx context.Context, v VM, fromID, toID int, exclude map[int]bool, limits ...Limit) (VM, error) {
	s.createMu.Lock()
	defer s.createMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VM{}, err
	}
	defer tx.Rollback()

	for _, l := range limits {
		n, err := countVMs(ctx, tx, l.TemplateID, l.OwnerID)
		if err != nil {
			return VM{}, err
		}
		if n >= l.Max {
			return VM{}, fmt.Errorf("%w: %s", ErrLimit, l.Message)
		}
	}

	used := map[int]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT vmid FROM vms WHERE vmid BETWEEN ? AND ?`, fromID, toID)
	if err != nil {
		return VM{}, err
	}
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return VM{}, err
		}
		used[id] = true
	}
	rows.Close()

	v.VMID = 0
	for id := fromID; id <= toID; id++ {
		if !used[id] && !exclude[id] {
			v.VMID = id
			break
		}
	}
	if v.VMID == 0 {
		return VM{}, ErrNoVMID
	}

	v.CreatedAt = time.Now().UTC().Truncate(time.Second)
	_, err = tx.ExecContext(ctx,
		`INSERT INTO vms (`+vmColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, '')`,
		v.ID, v.OwnerID, v.Name, v.TemplateID, v.Node, v.VMID, v.Cores, v.MemoryMiB, v.NetworkID,
		v.State, v.Step, v.Progress, v.Error, unix(v.CreatedAt), v.AgentIP, unixOrZero(v.ExpiresAt), v.Extends)
	if err != nil {
		return VM{}, err
	}
	return v, tx.Commit()
}

func (s *Store) VM(ctx context.Context, id string) (VM, error) {
	return scanVM(s.db.QueryRowContext(ctx, `SELECT `+vmColumns+` FROM vms WHERE id = ?`, id))
}

// VMs lists deployments, newest first; ownerID 0 lists everyone's.
func (s *Store) VMs(ctx context.Context, ownerID int64) ([]VM, error) {
	query := `SELECT ` + vmColumns + ` FROM vms`
	var args []any
	if ownerID != 0 {
		query += ` WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY created_at DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	vms := []VM{}
	for rows.Next() {
		v, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		vms = append(vms, v)
	}
	return vms, rows.Err()
}

// UpdateVMProgress records the state and step of a deployment or teardown.
func (s *Store) UpdateVMProgress(ctx context.Context, id string, state VMState, step string, progress float64, errMsg string) error {
	return expectRow(s.db.ExecContext(ctx,
		`UPDATE vms SET state = ?, step = ?, progress = ?, error = ? WHERE id = ?`,
		state, step, progress, errMsg, id))
}

// CountVMs counts deployments with templateID ("" for any) and ownerID (0
// for anyone's), except those being destroyed.
func (s *Store) CountVMs(ctx context.Context, templateID string, ownerID int64) (int, error) {
	return countVMs(ctx, s.db, templateID, ownerID)
}

func countVMs(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, templateID string, ownerID int64) (int, error) {
	query := `SELECT COUNT(*) FROM vms WHERE state != ?`
	args := []any{VMDestroying}
	if templateID != "" {
		query += ` AND template_id = ?`
		args = append(args, templateID)
	}
	if ownerID != 0 {
		query += ` AND owner_id = ?`
		args = append(args, ownerID)
	}
	var n int
	err := q.QueryRowContext(ctx, query, args...).Scan(&n)
	return n, err
}

// SetVMExpiry records a VM's expiry (zero for none) and extension count.
func (s *Store) SetVMExpiry(ctx context.Context, id string, expiresAt time.Time, extends int) error {
	return expectRow(s.db.ExecContext(ctx, `UPDATE vms SET expires_at = ?, extends = ? WHERE id = ?`,
		unixOrZero(expiresAt), extends, id))
}

// SetMissingExpiry gives VMs without an expiry (deployed while none was
// configured) one at expiresAt, returning how many it changed.
func (s *Store) SetMissingExpiry(ctx context.Context, expiresAt time.Time) (int64, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE vms SET expires_at = ? WHERE expires_at = 0`, unix(expiresAt))
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return unix(t)
}

func (s *Store) DeleteVM(ctx context.Context, id string) error {
	return expectRow(s.db.ExecContext(ctx, `DELETE FROM vms WHERE id = ?`, id))
}

// AssignAgentIP gives a VM the first address next yields (for i = 0, 1, ...)
// that no other VM uses. next returns false when the pool is exhausted.
func (s *Store) AssignAgentIP(ctx context.Context, id string, next func(i int) (string, bool)) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	used := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT agent_ip FROM vms WHERE agent_ip != ''`)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			rows.Close()
			return "", err
		}
		used[ip] = true
	}
	rows.Close()
	for i := 0; ; i++ {
		ip, ok := next(i)
		if !ok {
			return "", ErrNoAgentIP
		}
		if used[ip] {
			continue
		}
		if err := expectRow(tx.ExecContext(ctx, `UPDATE vms SET agent_ip = ? WHERE id = ?`, ip, id)); err != nil {
			return "", err
		}
		return ip, tx.Commit()
	}
}

// VMFirewall returns a VM's firewall settings as stored (JSON), empty if
// they were never set.
func (s *Store) VMFirewall(ctx context.Context, id string) (string, error) {
	var fw string
	err := s.db.QueryRowContext(ctx, `SELECT firewall FROM vms WHERE id = ?`, id).Scan(&fw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return fw, err
}

// SetVMFirewall stores a VM's firewall settings (JSON).
func (s *Store) SetVMFirewall(ctx context.Context, id, fw string) error {
	return expectRow(s.db.ExecContext(ctx, `UPDATE vms SET firewall = ? WHERE id = ?`, fw, id))
}
