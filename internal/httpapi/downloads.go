package httpapi

import (
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"quicksand/internal/agentclient"
	"quicksand/internal/auth"
	"quicksand/internal/vms"
)

// downloadTTL bounds how long a prepared download's link works.
const downloadTTL = time.Minute

// pendingDownload is an encrypted zip download prepared by a POST, so the
// password travels in a request body rather than the download's URL.
type pendingDownload struct {
	owner    int64
	guest    vms.GuestAgent
	file     agentclient.FileRef
	password string
	expires  time.Time
}

// downloads holds prepared downloads; each link works once.
type downloads struct {
	mu      sync.Mutex
	pending map[string]pendingDownload
}

func (d *downloads) add(p pendingDownload) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	for token, old := range d.pending {
		if now.After(old.expires) {
			delete(d.pending, token)
		}
	}
	if d.pending == nil {
		d.pending = map[string]pendingDownload{}
	}
	token := rand.Text()
	p.expires = now.Add(downloadTTL)
	d.pending[token] = p
	return token
}

func (d *downloads) take(token string, owner int64) (pendingDownload, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pending[token]
	if !ok || p.owner != owner || time.Now().After(p.expires) {
		return pendingDownload{}, false
	}
	delete(d.pending, token)
	return p, true
}

// handlePrepareZip prepares downloading a guest file or directory as a
// password-protected zip and returns the link to download it from.
func (s *Server) handlePrepareZip(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guestAgent(w, r)
	if !ok {
		return
	}
	var req struct {
		Path     string `json:"path"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, errors.New("a password is required"))
		return
	}
	auditDetail(r, "%s (as an encrypted zip)", req.Path)
	token := s.downloads.add(pendingDownload{
		owner: auth.FromContext(r.Context()).ID, guest: g, file: g.File(req.Path), password: req.Password,
	})
	writeJSON(w, http.StatusOK, map[string]string{"url": "/api/downloads/" + token})
}

// handlePreparedDownload streams a download prepared by handlePrepareZip.
func (s *Server) handlePreparedDownload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.downloads.take(r.PathValue("token"), auth.FromContext(r.Context()).ID)
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("this download link has expired"))
		return
	}
	resp, err := p.guest.DownloadZip(r.Context(), p.guest.Host, p.file, p.password)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	relayDownload(w, resp)
}

// relayDownload streams an agent's file response to the client.
func relayDownload(w http.ResponseWriter, resp *http.Response) {
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)
}
