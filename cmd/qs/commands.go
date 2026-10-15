package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"quicksand/internal/agentproto"
)

type vm struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	TemplateName string  `json:"templateName"`
	VMID         int     `json:"vmid"`
	Cores        int     `json:"cores"`
	MemoryMiB    int     `json:"memoryMiB"`
	NetworkLabel string  `json:"networkLabel"`
	State        string  `json:"state"`
	Step         string  `json:"step"`
	Progress     float64 `json:"progress"`
	Error        string  `json:"error"`
	Power        string  `json:"power"`
	Booting      bool    `json:"booting"`
	Action       string  `json:"action"`
	ActionError  string  `json:"actionError"`
	// ActionProgress is the running action's step; Done and Total are
	// bytes, when known.
	ActionProgress *struct {
		Step        string
		Done, Total int64
	} `json:"actionProgress"`
	Owner     string     `json:"owner"`
	Serial    bool       `json:"serial"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Snapshot  *struct {
		TakenAt time.Time `json:"takenAt"`
		Memory  bool      `json:"memory"`
	} `json:"snapshot"`
	ExtendsLeft *int `json:"extendsLeft"`
	Agent       *struct {
		IP       string `json:"ip"`
		Status   string `json:"status"`
		Hostname string `json:"hostname"`
		OS       string `json:"os"`
	} `json:"agent"`
}

// expiry is when the VM expires, as time left ("-" if never).
func (v vm) expiry() string {
	if v.ExpiresAt == nil {
		return "-"
	}
	left := time.Until(*v.ExpiresAt)
	if left <= 0 {
		return "now"
	}
	return "in " + formatLeft(left)
}

// formatLeft formats a duration like the web UI: "2d 4h", "3h 12m", "12m".
func formatLeft(d time.Duration) string {
	m := int(d.Minutes())
	switch {
	case m < 1:
		return "<1m"
	case m >= 1440 && m%1440/60 > 0:
		return fmt.Sprintf("%dd %dh", m/1440, m%1440/60)
	case m >= 1440:
		return fmt.Sprintf("%dd", m/1440)
	case m >= 60:
		return fmt.Sprintf("%dh %dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

// status summarises a VM's state like the web UI does.
func (v vm) status() string {
	switch {
	case v.State == "deploying" || v.State == "destroying":
		return fmt.Sprintf("%s %d%%", v.State, int(v.Progress*100))
	case v.State == "failed":
		return "failed"
	case v.Action != "":
		return v.Action + "…"
	case v.Power == "running" && v.Booting:
		return "booting"
	}
	return v.Power
}

func signedIn() (*client, error) {
	s, err := loadSettings()
	if err != nil {
		return nil, err
	}
	return newClient(s), nil
}

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	fingerprint := fs.String("fingerprint", "", "trust a server certificate with this SHA-256 fingerprint")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: qs login [-fingerprint SHA256] <server URL>")
	}
	server := strings.TrimRight(fs.Arg(0), "/")
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("invalid server URL %q", fs.Arg(0))
	}
	in := bufio.NewReader(os.Stdin)
	s := settings{Server: server, Fingerprint: normalizeFingerprint(*fingerprint)}
	if u.Scheme == "https" && s.Fingerprint == "" {
		if s.Fingerprint, err = checkCertificate(in, u.Host); err != nil {
			return err
		}
	}

	fmt.Fprint(os.Stderr, "Username: ")
	username, err := in.ReadString('\n')
	if err != nil {
		return err
	}
	password, err := readPassword(in)
	if err != nil {
		return err
	}
	c := newClient(s)
	if s.Session, err = c.login(strings.TrimSpace(username), password); err != nil {
		return err
	}
	if err := saveSettings(s); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Signed in to %s as %s.\n", server, strings.TrimSpace(username))
	return nil
}

func readPassword(in *bufio.Reader) (string, error) {
	fmt.Fprint(os.Stderr, "Password: ")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return string(b), err
	}
	line, err := in.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// checkCertificate connects to host and returns "" if the system trusts
// its certificate, or the fingerprint to pin once the user accepts it.
func checkCertificate(in *bufio.Reader, host string) (string, error) {
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{})
	if err == nil {
		conn.Close()
		return "", nil
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return "", err
	}
	raw, derr := tls.DialWithDialer(dialer, "tcp", host, &tls.Config{InsecureSkipVerify: true})
	if derr != nil {
		return "", derr
	}
	defer raw.Close()
	certs := raw.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", err
	}
	fp := certFingerprint(certs[0].Raw)
	fmt.Fprintf(os.Stderr, "The server's certificate isn't trusted by this system (%v).\n", err)
	fmt.Fprintf(os.Stderr, "Its SHA-256 fingerprint is\n  %s\n", formatFingerprint(fp))
	fmt.Fprint(os.Stderr, "Trust this certificate for this server? [y/N] ")
	answer, _ := in.ReadString('\n')
	if !strings.EqualFold(strings.TrimSpace(answer), "y") {
		return "", errors.New("certificate not trusted")
	}
	return fp, nil
}

func normalizeFingerprint(s string) string {
	return strings.ToUpper(strings.NewReplacer(":", "", " ", "").Replace(s))
}

func formatFingerprint(fp string) string {
	var parts []string
	for i := 0; i+2 <= len(fp); i += 2 {
		parts = append(parts, fp[i:i+2])
	}
	return strings.Join(parts, ":")
}

func cmdLogout() error {
	c, err := signedIn()
	if err != nil {
		return err
	}
	c.do("POST", "/api/auth/logout", nil, nil)
	p, err := settingsPath()
	if err != nil {
		return err
	}
	return os.Remove(p)
}

func cmdWhoami() error {
	c, err := signedIn()
	if err != nil {
		return err
	}
	var u struct {
		Username string `json:"username"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := c.do("GET", "/api/auth/me", nil, &u); err != nil {
		return err
	}
	role := ""
	if u.IsAdmin {
		role = " (administrator)"
	}
	fmt.Printf("%s%s on %s\n", u.Username, role, c.Server)
	return nil
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ExitOnError)
	all := fs.Bool("a", false, "list everyone's VMs (administrators only)")
	fs.Parse(args)
	c, err := signedIn()
	if err != nil {
		return err
	}
	path := "/api/vms"
	if *all {
		path += "?all=1"
	}
	var list []vm
	if err := c.do("GET", path, nil, &list); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	header := "NAME\tVMID\tSTATUS\tTEMPLATE\tCPUS\tMEMORY\tNETWORK\tAGENT"
	expiring := slices.ContainsFunc(list, func(v vm) bool { return v.ExpiresAt != nil })
	if expiring {
		header += "\tEXPIRES"
	}
	snapshots := slices.ContainsFunc(list, func(v vm) bool { return v.Snapshot != nil })
	if snapshots {
		header += "\tSNAPSHOT"
	}
	if *all {
		header += "\tOWNER"
	}
	fmt.Fprintln(w, header)
	for _, v := range list {
		agent := "-"
		if v.Agent != nil {
			agent = v.Agent.Status
		}
		line := fmt.Sprintf("%s\t%d\t%s\t%s\t%d\t%s\t%s\t%s", v.Name, v.VMID, v.status(), v.TemplateName, v.Cores,
			formatMiB(v.MemoryMiB), v.NetworkLabel, agent)
		if expiring {
			line += "\t" + v.expiry()
		}
		if snapshots {
			snap := "-"
			if v.Snapshot != nil {
				snap = formatLeft(time.Since(v.Snapshot.TakenAt)) + " ago"
			}
			line += "\t" + snap
		}
		if *all {
			line += "\t" + v.Owner
		}
		fmt.Fprintln(w, line)
	}
	return w.Flush()
}

func formatMiB(m int) string {
	if m >= 1024 && m%1024 == 0 {
		return fmt.Sprintf("%d GiB", m/1024)
	}
	return fmt.Sprintf("%d MiB", m)
}

func cmdTemplates() error {
	c, err := signedIn()
	if err != nil {
		return err
	}
	var list []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		OS           string `json:"os"`
		Cores        int    `json:"cores"`
		MemoryMiB    int    `json:"memoryMiB"`
		MaxCores     int    `json:"maxCores"`
		MaxMemoryMiB int    `json:"maxMemoryMiB"`
		Agent        bool   `json:"agent"`
		Error        string `json:"error"`
		QuotaBlocked string `json:"quotaBlocked"`
	}
	if err := c.do("GET", "/api/templates", nil, &list); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tOS\tCPUS\tMEMORY\tAGENT")
	for _, t := range list {
		cpus := fmt.Sprintf("%d (max %d)", t.Cores, t.MaxCores)
		mem := fmt.Sprintf("%s (max %s)", formatMiB(t.MemoryMiB), formatMiB(t.MaxMemoryMiB))
		if t.Error != "" {
			cpus, mem = "?", t.Error
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%v\n", t.ID, t.Name, t.OS, cpus, mem, t.Agent)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, t := range list {
		if t.QuotaBlocked != "" {
			fmt.Fprintf(os.Stderr, "%s: %s\n", t.ID, t.QuotaBlocked)
		}
	}
	return nil
}

func cmdNetworks() error {
	c, err := signedIn()
	if err != nil {
		return err
	}
	var list []struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Default bool   `json:"default"`
	}
	if err := c.do("GET", "/api/networks", nil, &list); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tLABEL\tDEFAULT")
	for _, n := range list {
		def := ""
		if n.Default {
			def = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", n.ID, n.Label, def)
	}
	return w.Flush()
}

func cmdDeploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	name := fs.String("name", "", "VM name (default: generated)")
	cpus := fs.Int("cpus", 0, "number of CPUs (default: the template's)")
	memory := fs.Int("memory", 0, "memory in MiB (default: the template's)")
	network := fs.String("network", "", "network ID (default: the default network)")
	noAgent := fs.Bool("no-agent", false, "deploy without the agent network, fully isolated from Quicksand")
	noWait := fs.Bool("no-wait", false, "return once the deployment has started")
	fs.Parse(reorder(args, map[string]bool{"name": true, "cpus": true, "memory": true, "network": true}))
	if fs.NArg() != 1 {
		return errors.New("usage: qs deploy [options] <template>")
	}
	c, err := signedIn()
	if err != nil {
		return err
	}
	req := map[string]any{"templateId": fs.Arg(0), "name": *name, "cores": *cpus, "memoryMiB": *memory, "networkId": *network}
	if *noAgent {
		req["agent"] = false
	}
	var v vm
	if err := c.do("POST", "/api/vms", req, &v); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Deploying %s (VM %d).\n", v.Name, v.VMID)
	if *noWait {
		return nil
	}
	return waitDeployed(c, v)
}

// waitDeployed polls a deployment until it's ready or failed.
func waitDeployed(c *client, v vm) error {
	tty := term.IsTerminal(int(os.Stderr.Fd()))
	last := ""
	for v.State == "deploying" {
		time.Sleep(time.Second)
		// A fresh value: fields the server leaves out when empty would keep
		// their old values.
		var now vm
		if err := c.do("GET", "/api/vms/"+url.PathEscape(v.ID), nil, &now); err != nil {
			return err
		}
		v = now
		line := fmt.Sprintf("%3d%% %s", int(v.Progress*100), v.Step)
		if line != last {
			if tty {
				fmt.Fprintf(os.Stderr, "\r\033[K%s", line)
			} else {
				fmt.Fprintln(os.Stderr, line)
			}
			last = line
		}
	}
	if tty {
		fmt.Fprint(os.Stderr, "\r\033[K")
	}
	if v.State == "failed" {
		return fmt.Errorf("deployment failed: %s", v.Error)
	}
	fmt.Fprintf(os.Stderr, "%s is ready.\n", v.Name)
	return nil
}

// reorder moves flags after positional arguments to the front, so
// "qs deploy win10 -cpus 4" works; valued names flags that take a value.
func reorder(args []string, valued map[string]bool) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if valued[name] && !strings.Contains(name, "=") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	return append(flags, rest...)
}

// findVM resolves a VM by Quicksand ID, Proxmox VM ID or name.
func findVM(c *client, ref string) (vm, error) {
	// Administrators can manage everyone's VMs; others get 403 here.
	var list []vm
	err := c.do("GET", "/api/vms?all=1", nil, &list)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == 403 {
		err = c.do("GET", "/api/vms", nil, &list)
	}
	if err != nil {
		return vm{}, err
	}
	if i := slices.IndexFunc(list, func(v vm) bool { return v.ID == ref }); i >= 0 {
		return list[i], nil
	}
	if n, err := strconv.Atoi(ref); err == nil {
		if i := slices.IndexFunc(list, func(v vm) bool { return v.VMID == n }); i >= 0 {
			return list[i], nil
		}
	}
	var matches []vm
	for _, v := range list {
		if v.Name == ref {
			matches = append(matches, v)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return vm{}, fmt.Errorf("no VM %q (see \"qs ls\")", ref)
	}
	return vm{}, fmt.Errorf("%d VMs are named %q; use the VM ID instead", len(matches), ref)
}

func vmArg(args []string, usage string) (*client, vm, error) {
	if len(args) != 1 {
		return nil, vm{}, errors.New("usage: " + usage)
	}
	c, err := signedIn()
	if err != nil {
		return nil, vm{}, err
	}
	v, err := findVM(c, args[0])
	return c, v, err
}

func cmdPower(action string, args []string) error {
	c, v, err := vmArg(args, "qs "+action+" <vm>")
	if err != nil {
		return err
	}
	if err := c.do("POST", "/api/vms/"+url.PathEscape(v.ID)+"/power", map[string]string{"action": action}, nil); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Sent %s to %s.\n", action, v.Name)
	return nil
}

func cmdExtend(args []string) error {
	c, v, err := vmArg(args, "qs extend <vm>")
	if err != nil {
		return err
	}
	if err := c.do("POST", "/api/vms/"+url.PathEscape(v.ID)+"/extend", nil, &v); err != nil {
		return err
	}
	left := ""
	if v.ExtendsLeft != nil {
		left = fmt.Sprintf(" (%d more extensions allowed)", *v.ExtendsLeft)
		if *v.ExtendsLeft == 1 {
			left = " (1 more extension allowed)"
		}
	}
	if v.ExpiresAt != nil {
		fmt.Fprintf(os.Stderr, "%s now expires %s, in %s%s.\n", v.Name, v.ExpiresAt.Local().Format("Mon Jan 2 15:04"), formatLeft(time.Until(*v.ExpiresAt)), left)
	}
	return nil
}

func cmdDestroy(args []string) error {
	fs := flag.NewFlagSet("destroy", flag.ExitOnError)
	yes := fs.Bool("y", false, "don't ask for confirmation")
	fs.Parse(reorder(args, nil))
	c, v, err := vmArg(fs.Args(), "qs destroy [-y] <vm>")
	if err != nil {
		return err
	}
	if !*yes {
		if err := confirm(fmt.Sprintf("Destroy %s (VM %d) and its disks? This can't be undone.", v.Name, v.VMID)); err != nil {
			return err
		}
	}
	if err := c.do("DELETE", "/api/vms/"+url.PathEscape(v.ID), nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Destroying %s.\n", v.Name)
	return nil
}

func cmdPs(args []string) error {
	c, v, err := vmArg(args, "qs ps <vm>")
	if err != nil {
		return err
	}
	var list agentproto.ProcessList
	if err := c.do("GET", "/api/vms/"+url.PathEscape(v.ID)+"/processes", nil, &list); err != nil {
		return err
	}
	slices.SortFunc(list.Processes, func(a, b agentproto.Process) int { return a.PID - b.PID })
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PID\tPPID\tUSER\tMEMORY\tCPU TIME\tNAME\tCOMMAND")
	for _, p := range list.Processes {
		cpu := time.Duration(p.CPUTimeMs) * time.Millisecond
		fmt.Fprintf(w, "%d\t%d\t%s\t%s\t%s\t%s\t%s\n", p.PID, p.PPID, p.User, formatBytes(p.Memory),
			cpu.Truncate(time.Second), p.Name, p.Command)
	}
	return w.Flush()
}

func formatBytes(b uint64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1fG", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.0fK", float64(b)/(1<<10))
	}
	return strconv.FormatUint(b, 10)
}

func cmdKill(args []string) error {
	fs := flag.NewFlagSet("kill", flag.ExitOnError)
	force := fs.Bool("f", false, "kill immediately (SIGKILL on Linux; Windows always does)")
	fs.Parse(reorder(args, nil))
	if fs.NArg() != 2 {
		return errors.New("usage: qs kill [-f] <vm> <pid>")
	}
	pid, err := strconv.Atoi(fs.Arg(1))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid pid %q", fs.Arg(1))
	}
	c, v, err := vmArg(fs.Args()[:1], "qs kill [-f] <vm> <pid>")
	if err != nil {
		return err
	}
	return c.do("POST", fmt.Sprintf("/api/vms/%s/processes/%d/kill", url.PathEscape(v.ID), pid),
		agentproto.KillRequest{Force: *force}, nil)
}

func cmdNetstat(args []string) error {
	c, v, err := vmArg(args, "qs netstat <vm>")
	if err != nil {
		return err
	}
	var list agentproto.ConnectionList
	if err := c.do("GET", "/api/vms/"+url.PathEscape(v.ID)+"/connections", nil, &list); err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROTO\tLOCAL\tREMOTE\tSTATE\tPID\tPROCESS")
	for _, conn := range list.Connections {
		pid := ""
		if conn.PID > 0 {
			pid = strconv.Itoa(conn.PID)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", conn.Proto, conn.Local, conn.Remote, conn.State, pid, conn.ProcessName)
	}
	return w.Flush()
}
