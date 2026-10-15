package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

var (
	// ErrNoLinkVLAN is returned when every link network VLAN tag is in use.
	ErrNoLinkVLAN = errors.New("no free link network VLAN tags")
	// ErrNoLinkIP is returned when the link subnet has no free addresses.
	ErrNoLinkIP = errors.New("no free link network addresses")
)

// MergeNeededError is returned when VMs to link are in different link
// networks, and merging them wasn't asked for.
type MergeNeededError struct{ Networks []LinkNetwork }

func (e *MergeNeededError) Error() string {
	return fmt.Sprintf("these VMs are in %d different networks; merge them to link", len(e.Networks))
}

// LinkNetwork is a private network VMs are joined into: a VLAN only its
// members' link NICs are on.
type LinkNetwork struct {
	ID        int64
	Name      string
	VLAN      int
	CreatedAt time.Time
}

// LinkAllocator hands out what new links need: VLANFrom..VLANTo are the
// tags networks may use, and NextIP returns the i'th candidate address (i =
// 0, 1, ...), false when the pool is exhausted.
type LinkAllocator struct {
	VLANFrom, VLANTo int
	NextIP           func(i int) (string, bool)
}

// LinkChange is the outcome of a link operation: the network the VMs are
// in now (zero when unlinked), and every VM whose network changed, whose
// link NIC must be updated.
type LinkChange struct {
	Network LinkNetwork
	Changed []VM
}

func (s *Store) LinkNetworks(ctx context.Context) ([]LinkNetwork, error) {
	return linkNetworks(ctx, s.db)
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func linkNetworks(ctx context.Context, q querier) ([]LinkNetwork, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, vlan, created_at FROM link_networks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LinkNetwork{}
	for rows.Next() {
		var n LinkNetwork
		var created int64
		if err := rows.Scan(&n.ID, &n.Name, &n.VLAN, &created); err != nil {
			return nil, err
		}
		n.CreatedAt = fromUnix(created)
		out = append(out, n)
	}
	return out, rows.Err()
}

// Link joins VMs into one link network: the one some are in already, a new
// one if none are, or, with merge, the oldest of several (the others'
// members move into it and they're deleted).
func (s *Store) Link(ctx context.Context, ids []string, merge bool, alloc LinkAllocator) (LinkChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LinkChange{}, err
	}
	defer tx.Rollback()
	networks, err := linkNetworks(ctx, tx)
	if err != nil {
		return LinkChange{}, err
	}
	var vms []VM
	var involved []LinkNetwork
	for _, id := range ids {
		v, err := scanVM(tx.QueryRowContext(ctx, `SELECT `+vmColumns+` FROM vms WHERE id = ?`, id))
		if err != nil {
			return LinkChange{}, err
		}
		vms = append(vms, v)
		if i := slices.IndexFunc(networks, func(n LinkNetwork) bool { return n.ID == v.LinkID }); i >= 0 &&
			!slices.ContainsFunc(involved, func(n LinkNetwork) bool { return n.ID == v.LinkID }) {
			involved = append(involved, networks[i])
		}
	}
	slices.SortFunc(involved, func(a, b LinkNetwork) int { return int(a.ID - b.ID) })
	if len(involved) > 1 && !merge {
		return LinkChange{}, &MergeNeededError{Networks: involved}
	}

	var target LinkNetwork
	if len(involved) > 0 {
		target = involved[0]
	} else if target, err = createLinkNetwork(ctx, tx, networks, alloc); err != nil {
		return LinkChange{}, err
	}

	// Members of the networks merged away move too.
	for _, n := range involved[min(1, len(involved)):] {
		rows, err := tx.QueryContext(ctx, `SELECT `+vmColumns+` FROM vms WHERE link_id = ?`, n.ID)
		if err != nil {
			return LinkChange{}, err
		}
		for rows.Next() {
			v, err := scanVM(rows)
			if err != nil {
				rows.Close()
				return LinkChange{}, err
			}
			if !slices.ContainsFunc(vms, func(o VM) bool { return o.ID == v.ID }) {
				vms = append(vms, v)
			}
		}
		rows.Close()
	}

	change := LinkChange{Network: target}
	for _, v := range vms {
		if v.LinkID == target.ID {
			continue
		}
		if v.LinkIP == "" {
			if v.LinkIP, err = freeLinkIP(ctx, tx, alloc.NextIP); err != nil {
				return LinkChange{}, err
			}
		}
		v.LinkID = target.ID
		if err := expectRow(tx.ExecContext(ctx, `UPDATE vms SET link_id = ?, link_ip = ? WHERE id = ?`,
			v.LinkID, v.LinkIP, v.ID)); err != nil {
			return LinkChange{}, err
		}
		change.Changed = append(change.Changed, v)
	}
	for _, n := range involved[min(1, len(involved)):] {
		if _, err := tx.ExecContext(ctx, `DELETE FROM link_networks WHERE id = ?`, n.ID); err != nil {
			return LinkChange{}, err
		}
	}
	return change, tx.Commit()
}

func createLinkNetwork(ctx context.Context, tx *sql.Tx, existing []LinkNetwork, alloc LinkAllocator) (LinkNetwork, error) {
	n := LinkNetwork{CreatedAt: time.Now()}
	for tag := alloc.VLANFrom; tag <= alloc.VLANTo && n.VLAN == 0; tag++ {
		if !slices.ContainsFunc(existing, func(e LinkNetwork) bool { return e.VLAN == tag }) {
			n.VLAN = tag
		}
	}
	if n.VLAN == 0 {
		return n, ErrNoLinkVLAN
	}
	// "Network 1", "Network 2", ...: the first free number.
	for i := 1; n.Name == ""; i++ {
		name := fmt.Sprintf("Network %d", i)
		if !slices.ContainsFunc(existing, func(e LinkNetwork) bool { return e.Name == name }) {
			n.Name = name
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO link_networks (name, vlan, created_at) VALUES (?, ?, ?)`,
		n.Name, n.VLAN, n.CreatedAt.Unix())
	if err != nil {
		return n, err
	}
	n.ID, err = res.LastInsertId()
	return n, err
}

func freeLinkIP(ctx context.Context, tx *sql.Tx, next func(i int) (string, bool)) (string, error) {
	used := map[string]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT link_ip FROM vms WHERE link_ip != ''`)
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
			return "", ErrNoLinkIP
		}
		if !used[ip] {
			return ip, nil
		}
	}
}

// Unlink takes a VM out of its link network. A network left with one
// member is deleted, and that member unlinked too.
func (s *Store) Unlink(ctx context.Context, id string) (LinkChange, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LinkChange{}, err
	}
	defer tx.Rollback()
	v, err := scanVM(tx.QueryRowContext(ctx, `SELECT `+vmColumns+` FROM vms WHERE id = ?`, id))
	if err != nil || v.LinkID == 0 {
		return LinkChange{}, err
	}
	network := v.LinkID
	rows, err := tx.QueryContext(ctx, `SELECT `+vmColumns+` FROM vms WHERE link_id = ? AND id != ?`, network, id)
	if err != nil {
		return LinkChange{}, err
	}
	var rest []VM
	for rows.Next() {
		o, err := scanVM(rows)
		if err != nil {
			rows.Close()
			return LinkChange{}, err
		}
		rest = append(rest, o)
	}
	rows.Close()
	leaving := []VM{v}
	if len(rest) < 2 {
		leaving = append(leaving, rest...)
	}
	var change LinkChange
	for _, o := range leaving {
		if _, err := tx.ExecContext(ctx, `UPDATE vms SET link_id = NULL WHERE id = ?`, o.ID); err != nil {
			return LinkChange{}, err
		}
		o.LinkID = 0
		change.Changed = append(change.Changed, o)
	}
	if len(rest) < 2 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM link_networks WHERE id = ?`, network); err != nil {
			return LinkChange{}, err
		}
	}
	return change, tx.Commit()
}

// RenameLinkNetwork names a link network.
func (s *Store) RenameLinkNetwork(ctx context.Context, id int64, name string) error {
	return expectRow(s.db.ExecContext(ctx, `UPDATE link_networks SET name = ? WHERE id = ?`, name, id))
}
