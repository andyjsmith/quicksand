package agent

import (
	"archive/zip"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"quicksand/internal/agentproto"
)

// resolvePath turns the request's path (absolute, or relative to its base
// directory) into a clean absolute path.
func resolvePath(r *http.Request) (string, error) {
	p := r.URL.Query().Get("path")
	if p == "" || p == "~" {
		p = baseDir(r)
	} else if !filepath.IsAbs(p) && !isDriveList(p) {
		p = filepath.Join(baseDir(r), p)
	}
	if isDriveList(p) {
		return p, nil
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("path must be absolute")
	}
	return filepath.Clean(p), nil
}

func baseDir(r *http.Request) string {
	if b := r.URL.Query().Get("base"); b != "" {
		return b
	}
	return defaultFilesDir()
}

func (a *Agent) handleListFiles(w http.ResponseWriter, r *http.Request) {
	p, err := resolvePath(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: err.Error()})
		return
	}
	if isDriveList(p) {
		writeJSON(w, http.StatusOK, agentproto.FileList{Path: p, Entries: driveEntries()})
		return
	}
	// Starting directories may not exist yet in a fresh guest.
	if p == filepath.Clean(baseDir(r)) {
		os.MkdirAll(p, 0o755)
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		writeFileError(w, err)
		return
	}
	list := agentproto.FileList{Path: p, Parent: parentDir(p), Entries: make([]agentproto.FileEntry, 0, len(entries))}
	for _, e := range entries {
		list.Entries = append(list.Entries, fileEntry(filepath.Join(p, e.Name()), e.Name()))
	}
	slices.SortFunc(list.Entries, func(a, b agentproto.FileEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	writeJSON(w, http.StatusOK, list)
}

// fileEntry describes path, following symlinks for its type and size.
func fileEntry(path, name string) agentproto.FileEntry {
	e := agentproto.FileEntry{Name: name, Path: path}
	info, err := os.Lstat(path)
	if err != nil {
		return e
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		e.Symlink = true
		if target, err := os.Stat(path); err == nil {
			info = target
		}
	}
	e.Dir = info.IsDir()
	if !e.Dir {
		e.Size = info.Size()
	}
	e.ModTime = info.ModTime().UTC()
	e.Mode = info.Mode().String()
	return e
}

// parentDir returns the directory above p, empty at the top. On Windows
// drive roots lead to the drive list.
func parentDir(p string) string {
	parent := filepath.Dir(p)
	if parent != p {
		return parent
	}
	return driveListParent()
}

func (a *Agent) handleDownload(w http.ResponseWriter, r *http.Request) {
	p, err := resolvePath(r)
	if err != nil || isDriveList(p) {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid path"})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeFileError(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeFileError(w, err)
		return
	}
	name := filepath.Base(p)
	if info.IsDir() {
		slog.Info("download directory", "path", p, "from", r.RemoteAddr)
		f.Close()
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", attachment(name+".zip"))
		w.WriteHeader(http.StatusOK)
		if err := writeZip(w, p, ""); err != nil {
			slog.Warn("zip directory", "path", p, "err", err)
		}
		return
	}
	slog.Info("download", "path", p, "from", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", attachment(name))
	// ServeContent handles Content-Length, Last-Modified and ranges.
	http.ServeContent(w, r, "", info.ModTime(), f)
}

func attachment(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}

// writeZip streams root, a file or a directory (its contents at the top of
// the archive), as a zip archive, skipping what it can't read. A password
// encrypts every file with ZipCrypto.
func writeZip(w io.Writer, root, password string) error {
	zw := zip.NewWriter(w)
	create := func(fh *zip.FileHeader) (io.Writer, error) {
		fh.Method = zip.Deflate
		return zw.CreateHeader(fh)
	}
	if password != "" {
		create = encryptEntries(zw, password)
	}
	addFile := func(path, name string, info fs.FileInfo) error {
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return nil
		}
		hdr.Name = name
		out, err := create(hdr)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, f)
		return err
	}

	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		err = addFile(root, filepath.Base(root), info)
	} else {
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || path == root {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil
			}
			name := filepath.ToSlash(rel)
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if d.IsDir() {
				_, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Modified: info.ModTime()})
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			return addFile(path, name, info)
		})
	}
	if err != nil {
		return err
	}
	return zw.Close()
}

// handleZip streams a file or directory as a password-protected (ZipCrypto)
// zip archive. The password comes in the body (agentproto.ZipRequest).
func (a *Agent) handleZip(w http.ResponseWriter, r *http.Request) {
	p, err := resolvePath(r)
	if err != nil || isDriveList(p) {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid path"})
		return
	}
	var req agentproto.ZipRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "a password is required"})
		return
	}
	if _, err := os.Stat(p); err != nil {
		writeFileError(w, err)
		return
	}
	slog.Info("download encrypted zip", "path", p, "from", r.RemoteAddr)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachment(filepath.Base(p)+".zip"))
	w.WriteHeader(http.StatusOK)
	if err := writeZip(w, p, req.Password); err != nil {
		slog.Warn("zip", "path", p, "err", err)
	}
}

// handleUpload writes the request body to a file, through a temporary file
// so an interrupted upload leaves nothing behind.
func (a *Agent) handleUpload(w http.ResponseWriter, r *http.Request) {
	p, err := resolvePath(r)
	if err != nil || isDriveList(p) || parentDir(p) == "" {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid path"})
		return
	}
	if info, err := os.Stat(p); err == nil {
		if info.IsDir() {
			writeJSON(w, http.StatusConflict, agentproto.Error{Error: "a directory with that name exists"})
			return
		}
		if r.URL.Query().Get("overwrite") != "1" {
			writeJSON(w, http.StatusConflict, agentproto.Error{Error: "the file exists"})
			return
		}
	}
	suffix := make([]byte, 6)
	rand.Read(suffix)
	tmp := filepath.Join(filepath.Dir(p), "."+filepath.Base(p)+".upload-"+hex.EncodeToString(suffix))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		writeFileError(w, err)
		return
	}
	slog.Info("upload", "path", p, "size", r.ContentLength, "from", r.RemoteAddr)
	_, err = io.Copy(f, r.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		os.Remove(tmp)
		writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fileEntry(p, filepath.Base(p)))
}

func (a *Agent) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	p, err := resolvePath(r)
	if err != nil || isDriveList(p) || parentDir(p) == "" || filepath.Dir(p) == p {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "refusing to delete that path"})
		return
	}
	slog.Info("delete", "path", p, "from", r.RemoteAddr)
	if r.URL.Query().Get("recursive") == "1" {
		if _, err = os.Lstat(p); err == nil {
			err = os.RemoveAll(p)
		}
	} else {
		err = os.Remove(p)
	}
	if err != nil {
		writeFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *Agent) handleMkdir(w http.ResponseWriter, r *http.Request) {
	p, err := resolvePath(r)
	if err != nil || isDriveList(p) {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid path"})
		return
	}
	if err := os.Mkdir(p, 0o755); err != nil {
		writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fileEntry(p, filepath.Base(p)))
}

func writeFileError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, fs.ErrNotExist):
		status = http.StatusNotFound
	case errors.Is(err, fs.ErrPermission):
		status = http.StatusForbidden
	case errors.Is(err, fs.ErrExist):
		status = http.StatusConflict
	}
	// Drop the "open <path>:" prefix; the client knows the path.
	msg := err.Error()
	var pe *fs.PathError
	if errors.As(err, &pe) {
		msg = pe.Err.Error()
	}
	writeJSON(w, status, agentproto.Error{Error: msg})
}

func (a *Agent) handleHash(w http.ResponseWriter, r *http.Request) {
	p, err := resolvePath(r)
	if err != nil || isDriveList(p) {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "invalid path"})
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeFileError(w, err)
		return
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.IsDir() {
		writeJSON(w, http.StatusBadRequest, agentproto.Error{Error: "not a file"})
		return
	}
	h5, h1, h256 := md5.New(), sha1.New(), sha256.New()
	n, err := io.Copy(io.MultiWriter(h5, h1, h256), f)
	if err != nil {
		writeFileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, agentproto.FileHashes{
		Path: p, Size: n,
		MD5: hex.EncodeToString(h5.Sum(nil)), SHA1: hex.EncodeToString(h1.Sum(nil)), SHA256: hex.EncodeToString(h256.Sum(nil)),
	})
}
