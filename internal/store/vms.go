package store

import (
	"context"
	"database/sql"
	"errors"
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
}

const vmColumns = `id, owner_id, name, template_id, node, vmid, cores, memory_mib, network_id,
	state, step, progress, error, created_at, agent_ip`

func scanVM(row interface{ Scan(...any) error }) (VM, error) {
	var v VM
	var created int64
	err := row.Scan(&v.ID, &v.OwnerID, &v.Name, &v.TemplateID, &v.Node, &v.VMID, &v.Cores, &v.MemoryMiB,
		&v.NetworkID, &v.State, &v.Step, &v.Progress, &v.Error, &created, &v.AgentIP)
	if errors.Is(err, sql.ErrNoRows) {
		return VM{}, ErrNotFound
	}
	v.CreatedAt = fromUnix(created)
	return v, err
}

// CreateVM records a new deployment, assigning it the lowest free VM ID in
// [fromID, toID] that is not in use by another deployment or in exclude
// (IDs Proxmox already has).
func (s *Store) CreateVM(ctx context.Context, v VM, fromID, toID int, exclude map[int]bool) (VM, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return VM{}, err
	}
	defer tx.Rollback()

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
		`INSERT INTO vms (`+vmColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.OwnerID, v.Name, v.TemplateID, v.Node, v.VMID, v.Cores, v.MemoryMiB, v.NetworkID,
		v.State, v.Step, v.Progress, v.Error, unix(v.CreatedAt), v.AgentIP)
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
