package vms

import (
	"testing"
	"time"

	"quicksand/internal/config"
)

func TestSuspendAt(t *testing.T) {
	cfg := &config.Config{Suspend: &config.Suspend{IdleTime: config.Duration(time.Hour)}}
	s := NewService(cfg, nil, nil, nil, nil)
	near := func(at, want time.Time) bool { return at.Sub(want).Abs() < 2*time.Second }

	if at := s.suspendAt("a"); !near(at, time.Now().Add(time.Hour)) {
		t.Errorf("new VM suspends at %v, want an hour from now", at)
	}
	done := s.Use("a")
	other := s.Use("a")
	if at := s.suspendAt("a"); !at.IsZero() {
		t.Errorf("VM in use suspends at %v", at)
	}
	done()
	done()
	if at := s.suspendAt("a"); !at.IsZero() {
		t.Errorf("VM with a session still open suspends at %v", at)
	}
	s.mu.Lock()
	s.usage["a"].last = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	other()
	if at := s.suspendAt("a"); !near(at, time.Now().Add(time.Hour)) {
		t.Errorf("VM whose last session just closed suspends at %v, want an hour from now", at)
	}
	s.mu.Lock()
	s.usage["a"].last = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()
	s.resetIdle("a")
	if at := s.suspendAt("a"); !near(at, time.Now().Add(time.Hour)) {
		t.Errorf("restarted VM suspends at %v, want an hour from now", at)
	}

	if at := NewService(&config.Config{}, nil, nil, nil, nil).suspendAt("a"); !at.IsZero() {
		t.Errorf("VM suspends at %v without suspension configured", at)
	}
}
