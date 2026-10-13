package main

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sys/windows/svc"
)

// serviceName is the Windows service the installer registers.
const serviceName = "QuicksandAgent"

// runAsService runs the agent under the Windows service control manager.
// It reports false when the process wasn't started as a service.
func runAsService(run func(ctx context.Context) error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, err
	}
	return true, svc.Run(serviceName, &service{run: run})
}

type service struct {
	run func(ctx context.Context) error
}

func (s *service) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case err := <-done:
			if err != nil {
				slog.Error("agent stopped", "err", err)
				// A service-specific exit code makes the service manager apply
				// the recovery actions (restart) the installer configures.
				return true, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
				}
				return false, 0
			}
		}
	}
}

func isService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}
