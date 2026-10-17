package httpapi

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"

	"quicksand/internal/auth"
	"quicksand/internal/store"
)

// auditKey carries the request's pending audit entry, which handlers
// complete with auditDetail and auditTarget.
type auditKey struct{}

type pendingAudit struct {
	entry store.AuditEntry
	skip  bool
	done  bool
}

// audited records action in the audit log when h succeeds. The entry is
// written when the response starts, so long-lived streams (shells,
// consoles) are logged when they open. Its target is the VM, user or link
// the path names; handlers can add details.
func (s *Server) audited(action string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p := &pendingAudit{entry: store.AuditEntry{Action: action}}
		s.auditTargetFromPath(r, p)
		r = r.WithContext(context.WithValue(r.Context(), auditKey{}, p))
		aw := &auditWriter{ResponseWriter: w, start: func(status int) {
			if !p.done && !p.skip && status < 400 {
				s.audit(r, p.entry)
			}
			p.done = true
		}}
		h(aw, r)
		aw.begin(http.StatusOK) // nothing written: net/http sends a 200
	}
}

// auditTargetFromPath names what the request's path refers to.
func (s *Server) auditTargetFromPath(r *http.Request, p *pendingAudit) {
	id := r.PathValue("id")
	if id == "" {
		return
	}
	ctx, actor := r.Context(), auth.FromContext(r.Context())
	switch {
	case strings.HasPrefix(p.entry.Action, "vm."), strings.HasPrefix(p.entry.Action, "agent."):
		if vm, err := s.store.VM(ctx, id); err == nil {
			p.entry.Target = vm.Name
			if vm.OwnerID != actor.ID {
				if owner, err := s.store.User(ctx, vm.OwnerID); err == nil {
					p.entry.Target += " (" + owner.Username + "'s)"
				}
			}
		}
	case strings.HasPrefix(p.entry.Action, "user."):
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			if u, err := s.store.User(ctx, n); err == nil {
				p.entry.Target = u.Username
			}
		}
	default:
		p.entry.Target = id
	}
}

// auditDetail sets the details of the request's audit entry.
func auditDetail(r *http.Request, format string, args ...any) {
	if p, ok := r.Context().Value(auditKey{}).(*pendingAudit); ok {
		p.entry.Detail = fmt.Sprintf(format, args...)
	}
}

// auditTarget sets what the request's audit entry is about.
func auditTarget(r *http.Request, target string) {
	if p, ok := r.Context().Value(auditKey{}).(*pendingAudit); ok {
		p.entry.Target = target
	}
}

// skipAudit leaves a request that reports its failure in a successful
// response (such as a websocket closed with an error) out of the log.
func skipAudit(r *http.Request) {
	if p, ok := r.Context().Value(auditKey{}).(*pendingAudit); ok {
		p.skip = true
	}
}

// audit writes an entry for the request's user (unless the entry names
// one, as for sign-ins).
func (s *Server) audit(r *http.Request, e store.AuditEntry) {
	if e.Username == "" {
		u := auth.FromContext(r.Context())
		e.UserID, e.Username = u.ID, u.Username
	}
	e.IP = clientIP(r)
	// The request's context may already be ending (as when a stream starts).
	if err := s.store.AddAudit(context.WithoutCancel(r.Context()), e); err != nil {
		slog.Error("audit log", "action", e.Action, "err", err)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// auditWriter calls start with the status when the response starts,
// including when the connection is taken over for a websocket or stream.
type auditWriter struct {
	http.ResponseWriter
	start   func(status int)
	started bool
}

func (w *auditWriter) begin(status int) {
	if !w.started {
		w.started = true
		w.start(status)
	}
}

func (w *auditWriter) WriteHeader(status int) {
	w.begin(status)
	w.ResponseWriter.WriteHeader(status)
}

func (w *auditWriter) Write(b []byte) (int, error) {
	w.begin(http.StatusOK)
	return w.ResponseWriter.Write(b)
}

func (w *auditWriter) Flush() {
	w.begin(http.StatusOK)
	http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *auditWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("the connection can't be taken over")
	}
	w.begin(http.StatusSwitchingProtocols)
	return h.Hijack()
}

func (w *auditWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
