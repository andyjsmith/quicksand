package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
)

// snapshotTake snapshots a VM, replacing its snapshot (each VM has one).
func snapshotTake(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs snapshot take [--disk-only] [--force] <vm>")
	if err != nil {
		return err
	}
	if v.Snapshot != nil && !cmd.Bool("force") {
		if err := confirm(fmt.Sprintf("Replace the snapshot of %s from %s?", v.Name, snapshotTime(v))); err != nil {
			return err
		}
	}
	// Memory only exists while the VM runs.
	memory := !cmd.Bool("disk-only") && v.Power == "running"
	if err := c.do("POST", snapshotPath(v), map[string]bool{"memory": memory}, nil); err != nil {
		return err
	}
	what := "disks"
	if memory {
		what = "disks and memory"
	}
	return waitAction(c, v, fmt.Sprintf("Snapshotting %s (%s)", v.Name, what))
}

// snapshotRevert returns a VM to its snapshot, which is kept.
func snapshotRevert(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs snapshot revert [--force] <vm>")
	if err != nil {
		return err
	}
	if v.Snapshot == nil {
		return fmt.Errorf("%s has no snapshot", v.Name)
	}
	if !cmd.Bool("force") {
		if err := confirm(fmt.Sprintf("Revert %s to its snapshot from %s? Everything since is lost.", v.Name, snapshotTime(v))); err != nil {
			return err
		}
	}
	if err := c.do("POST", snapshotPath(v)+"/revert", nil, nil); err != nil {
		return err
	}
	return waitAction(c, v, "Reverting "+v.Name)
}

func snapshotDelete(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs snapshot delete [--force] <vm>")
	if err != nil {
		return err
	}
	if v.Snapshot == nil {
		return fmt.Errorf("%s has no snapshot", v.Name)
	}
	if !cmd.Bool("force") {
		if err := confirm(fmt.Sprintf("Delete the snapshot of %s from %s?", v.Name, snapshotTime(v))); err != nil {
			return err
		}
	}
	if err := c.do("DELETE", snapshotPath(v), nil, nil); err != nil {
		return err
	}
	return waitAction(c, v, "Deleting the snapshot of "+v.Name)
}

func snapshotPath(v vm) string { return "/api/vms/" + url.PathEscape(v.ID) + "/snapshot" }

func snapshotTime(v vm) string { return v.Snapshot.TakenAt.Local().Format("Mon Jan 2 15:04") }

// confirm asks a yes/no question on the terminal; anything but y cancels.
func confirm(question string) error {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		return errors.New("cancelled")
	}
	return nil
}

// waitAction waits for a VM's action (power or snapshot) to finish,
// showing its steps and a bar for those with a size, and reporting its error.
func waitAction(c *client, v vm, doing string) error {
	fmt.Fprintf(os.Stderr, "%s…\n", doing)
	var step string
	var bar *progress
	// endBar ends the bar of a step, full when the step completed: a
	// snapshot saves less memory than the RAM it's measured against.
	endBar := func(completed bool) {
		if bar != nil {
			if completed {
				bar.n = bar.total
			}
			bar.done()
			bar = nil
		}
	}
	for {
		time.Sleep(time.Second)
		// A fresh value each time: fields the server leaves out once they're
		// empty (the action) would otherwise keep their old values.
		var now vm
		if err := c.do("GET", "/api/vms/"+url.PathEscape(v.ID), nil, &now); err != nil {
			endBar(false)
			return err
		}
		if v = now; v.Action == "" {
			break
		}
		p := v.ActionProgress
		if p == nil {
			continue
		}
		if p.Step != step {
			step = p.Step
			endBar(true)
			if p.Total > 0 {
				bar = newProgress(nil, "  "+step, p.Total)
			}
			if bar == nil || !bar.tty {
				fmt.Fprintf(os.Stderr, "  %s…\n", step)
			}
		}
		if bar != nil {
			if p.Done < 1<<20 {
				// Still setting up (Proxmox first creates the volume the
				// memory goes to): the rate counts from when data flows.
				bar.start = time.Now()
			}
			bar.n, bar.total = p.Done, p.Total
			if bar.tty {
				bar.print()
			}
		}
	}
	endBar(v.ActionError == "")
	if v.ActionError != "" {
		return errors.New(v.ActionError)
	}
	fmt.Fprintln(os.Stderr, "Done.")
	return nil
}
