package vms

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"slices"

	"quicksand/internal/agentproto"
	"quicksand/internal/store"
)

// ErrNoSerial is returned for serial shells on VMs whose template doesn't
// enable them, or that lack the port.
var ErrNoSerial = errors.New("this VM has no serial console shell")

// serialPort returns the serial port shells use, if the VM's template
// enables serial shells and the VM has the port.
func (s *Service) serialPort(ctx context.Context, vm store.VM) (string, error) {
	t, ok := s.cfg.Template(vm.TemplateID)
	if !ok || t.Serial == "" {
		return "", ErrNoSerial
	}
	info, ok := s.consoleInfo(ctx, vm)
	if !ok || !slices.Contains(info.serial, t.Serial) {
		return "", ErrNoSerial
	}
	return t.Serial, nil
}

// SerialShell connects to the VM's serial console and returns a stream
// speaking the agent's shell frames (see agentproto.ReadFrame), so clients
// treat it like a terminal shell. It ends with FrameExit when the console
// connection closes.
func (s *Service) SerialShell(ctx context.Context, vm store.VM, cols, rows int) (net.Conn, error) {
	if vm.State != store.VMReady {
		return nil, ErrNotReady
	}
	port, err := s.serialPort(ctx, vm)
	if err != nil {
		return nil, err
	}
	t, err := s.pve.TermProxy(ctx, vm.Node, vm.VMID, port)
	if err != nil {
		return nil, err
	}
	term, err := s.pve.DialTerm(ctx, vm.Node, vm.VMID, t)
	if err != nil {
		return nil, err
	}
	if cols > 0 && rows > 0 {
		term.Resize(cols, rows)
	}
	slog.Info("serial shell opened", "vm", vm.ID, "vmid", vm.VMID, "port", port)

	client, conn := net.Pipe()
	go func() {
		// Frames from the client become terminal input.
		defer term.Close()
		for {
			typ, payload, err := agentproto.ReadFrame(conn)
			if err != nil {
				return
			}
			switch typ {
			case agentproto.FrameStdin:
				err = term.Input(payload)
			case agentproto.FrameResize:
				var rs agentproto.Resize
				if json.Unmarshal(payload, &rs) == nil && rs.Cols > 0 && rs.Rows > 0 {
					err = term.Resize(rs.Cols, rs.Rows)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer conn.Close()
		fw := agentproto.NewFrameWriter(conn)
		out := fw.StreamWriter(agentproto.FrameStdout)
		for {
			p, err := term.ReadOutput()
			if err != nil {
				fw.WriteJSON(agentproto.FrameExit, agentproto.ShellExit{Code: 0})
				slog.Info("serial shell closed", "vm", vm.ID, "reason", err)
				return
			}
			if _, err := out.Write(p); err != nil {
				term.Close()
				return
			}
		}
	}()
	return client, nil
}
