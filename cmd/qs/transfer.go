package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"quicksand/internal/agentproto"
)

// Remote paths are absolute or relative to the guest's files directory
// ("." is the directory itself).

// guestJoin joins a guest directory and a name with the guest's separator.
func guestJoin(dir, name string) string {
	sep := "/"
	if strings.Contains(dir, `\`) || len(dir) >= 2 && dir[1] == ':' {
		sep = `\`
	}
	if strings.HasSuffix(dir, sep) {
		return dir + name
	}
	return dir + sep + name
}

// guestBase is the last element of a guest path ("C" for C:\).
func guestBase(p string) string {
	p = strings.TrimRight(p, `/\`)
	name := strings.TrimSuffix(p[strings.LastIndexAny(p, `/\`)+1:], ":")
	if name == "" {
		return "root"
	}
	return name
}

func cmdDownload(ctx context.Context, cmd *cli.Command) error {
	recursive, extract := cmd.Bool("r"), cmd.Bool("u")
	password, encrypt := cmd.String("e"), cmd.IsSet("e")
	args := cmd.Args().Slice()
	switch {
	case len(args) < 2 || len(args) > 3:
		return errors.New("usage: qs download [-r [-u]] [-e password] <vm> <remote path> [local path]")
	case encrypt && password == "":
		return errors.New("-e needs a password")
	case extract && !recursive:
		return errors.New("-u extracts directories, so it needs -r")
	case extract && encrypt:
		return errors.New("-u and -e can't be combined: extracting would leave nothing encrypted")
	}
	if password == "-" {
		var err error
		if password, err = promptNewPassword("Zip password"); err != nil {
			return err
		}
	}
	vmRef, remote := args[0], args[1]
	local := "."
	if len(args) == 3 {
		local = args[2]
	}
	c, err := signedIn()
	if err != nil {
		return err
	}
	v, err := findVM(c, vmRef)
	if err != nil {
		return err
	}
	base := "/api/vms/" + url.PathEscape(v.ID)

	// Directories list; files don't.
	var list agentproto.FileList
	isDir := c.do("GET", base+"/files?"+url.Values{"path": {remote}}.Encode(), nil, &list) == nil
	if isDir && !recursive {
		return fmt.Errorf("%s is a directory; use -r", remote)
	}
	if isDir {
		remote = list.Path
	}
	name := guestBase(remote)

	var resp *http.Response
	if encrypt {
		var prep struct {
			URL string `json:"url"`
		}
		if err := c.do("POST", base+"/files/zip", map[string]string{"path": remote, "password": password}, &prep); err != nil {
			return err
		}
		resp, err = c.send("GET", prep.URL, nil, 0, "")
	} else {
		resp, err = c.send("GET", base+"/files/content?"+url.Values{"path": {remote}}.Encode(), nil, 0, "")
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if isDir || encrypt {
		name += ".zip"
	}
	body := newProgress(resp.Body, name, resp.ContentLength)
	defer body.done()

	if extract {
		dest := local
		if info, err := os.Stat(local); err == nil && info.IsDir() {
			dest = filepath.Join(local, guestBase(remote))
		}
		files := 0
		if err := extractZip(body, dest, func(string) { files++ }); err != nil {
			return err
		}
		body.done()
		fmt.Fprintf(os.Stderr, "Extracted %d files into %s.\n", files, dest)
		return nil
	}
	if local == "-" {
		_, err := io.Copy(os.Stdout, body)
		return err
	}
	target := local
	if info, err := os.Stat(local); err == nil && info.IsDir() {
		target = filepath.Join(local, name)
	}
	return writeFileAtomic(target, body)
}

// promptNewPassword asks for a new password twice, without echoing it. If
// standard input isn't a terminal, it reads one line from it instead.
func promptNewPassword(what string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if line = strings.TrimRight(line, "\r\n"); line == "" {
			return "", fmt.Errorf("no password on standard input: %v", err)
		}
		return line, nil
	}
	fmt.Fprint(os.Stderr, what+": ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if len(first) == 0 {
		return "", errors.New("the password can't be empty")
	}
	fmt.Fprint(os.Stderr, "Repeat "+strings.ToLower(what[:1])+what[1:]+": ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", errors.New("the passwords don't match")
	}
	return string(first), nil
}

// writeFileAtomic writes r to path through a temporary file, so a failed
// transfer leaves nothing half-written behind.
func writeFileAtomic(path string, r io.Reader) error {
	tmp := path + ".qs-part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

func cmdUpload(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()
	if len(args) < 2 {
		return errors.New("usage: qs upload <vm> <local file> [remote path]\n       qs upload <vm> <local file>... <remote path>")
	}
	// Without a remote path, one file goes to the files directory ("").
	vmRef, sources, remote := args[0], args[1:2], ""
	if len(args) > 2 {
		sources, remote = args[1:len(args)-1], args[len(args)-1]
	}
	for _, src := range sources {
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s isn't a file; only files can be uploaded", src)
		}
	}
	c, err := signedIn()
	if err != nil {
		return err
	}
	v, err := findVM(c, vmRef)
	if err != nil {
		return err
	}
	base := "/api/vms/" + url.PathEscape(v.ID)

	// Into a directory, files keep their names (like scp).
	var list agentproto.FileList
	destIsDir := c.do("GET", base+"/files?"+url.Values{"path": {remote}}.Encode(), nil, &list) == nil
	if len(sources) > 1 && !destIsDir {
		return fmt.Errorf("%s isn't a directory in the guest", remote)
	}
	for _, src := range sources {
		target := remote
		if destIsDir {
			target = guestJoin(list.Path, filepath.Base(src))
		}
		if err := uploadFile(c, base, src, target); err != nil {
			return fmt.Errorf("%s: %w", src, err)
		}
	}
	return nil
}

func uploadFile(c *client, base, src, target string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	body := newProgress(f, filepath.Base(src), info.Size())
	q := url.Values{"path": {target}, "overwrite": {"1"}}
	resp, err := c.send("PUT", base+"/files/content?"+q.Encode(), body, info.Size(), "application/octet-stream")
	body.done()
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// progress reports a transfer's progress on stderr when it's a terminal.
type progress struct {
	r          io.Reader
	name       string
	total, n   int64
	start, at  time.Time
	tty, ended bool
}

func newProgress(r io.Reader, name string, total int64) *progress {
	return &progress{r: r, name: name, total: total, start: time.Now(), tty: term.IsTerminal(int(os.Stderr.Fd()))}
}

func (p *progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.n += int64(n)
	if p.tty && time.Since(p.at) > 200*time.Millisecond {
		p.at = time.Now()
		p.print()
	}
	return n, err
}

func (p *progress) print() {
	elapsed := time.Since(p.start).Seconds()
	rate := 0.0
	if elapsed > 0.5 {
		rate = float64(p.n) / elapsed
	}
	var stats string
	if p.total > 0 {
		frac := min(float64(p.n)/float64(p.total), 1)
		const width = 24
		filled := int(frac * width)
		bar := strings.Repeat("=", filled)
		if filled < width {
			bar += ">" + strings.Repeat(" ", width-filled-1)
		}
		stats = fmt.Sprintf(" [%s] %3d%%  %s / %s", bar, int(frac*100), formatBytes(uint64(p.n)), formatBytes(uint64(p.total)))
	} else {
		stats = "  " + formatBytes(uint64(p.n))
	}
	if rate > 0 {
		stats += fmt.Sprintf("  %s/s", formatBytes(uint64(rate)))
		if p.total > 0 && p.n < p.total {
			eta := time.Duration(float64(p.total-p.n)/rate) * time.Second
			stats += "  ETA " + eta.Truncate(time.Second).String()
		} else if p.ended {
			stats += "  in " + time.Since(p.start).Truncate(time.Second).String()
		}
	}
	// Keep to one line, or the next \r can't overwrite it.
	name := p.name
	if cols, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil {
		switch room := cols - 1 - len(stats); {
		case room < 4:
			name = ""
		case len(name) > room:
			name = "…" + name[len(name)-room+1:]
		}
	}
	fmt.Fprintf(os.Stderr, "\r\033[K%s%s", name, stats)
}

// done ends the progress line.
func (p *progress) done() {
	if p.tty && !p.ended {
		p.ended = true
		p.print()
		fmt.Fprintln(os.Stderr)
	}
}
