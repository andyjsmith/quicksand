package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"quicksand/internal/agentproto"
	"quicksand/internal/version"
)

type vm struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	TemplateID   string  `json:"templateId"`
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
	Link        *struct {
		Network int64  `json:"network"`
		Name    string `json:"name"`
		Address string `json:"address"`
		MAC     string `json:"mac"`
		Status  string `json:"status"`
	} `json:"link"`
	Agent *struct {
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

func cmdLogin(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 1 {
		return errors.New("usage: qs login [--password | --token] [--fingerprint SHA256] <server URL>")
	}
	server := strings.TrimRight(cmd.Args().First(), "/")
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("invalid server URL %q", cmd.Args().First())
	}
	in := bufio.NewReader(os.Stdin)
	s := settings{Server: server, Fingerprint: normalizeFingerprint(cmd.String("fingerprint"))}
	if u.Scheme == "https" && s.Fingerprint == "" {
		if s.Fingerprint, err = checkCertificate(in, u.Host); err != nil {
			return err
		}
	}

	c := newClient(s)
	if cmd.Bool("token") {
		return loginWithToken(c, in)
	}
	methods, err := c.authMethods()
	if err != nil {
		return err
	}
	var username string
	switch {
	case methods.SSO != nil && (!cmd.Bool("password") || !methods.Password):
		if s.Token, username, err = c.ssoLogin(methods.SSO.Name); err != nil {
			return err
		}
	case methods.Password:
		fmt.Fprint(os.Stderr, "Username: ")
		if username, err = in.ReadString('\n'); err != nil {
			return err
		}
		username = strings.TrimSpace(username)
		password, err := readPassword(in)
		if err != nil {
			return err
		}
		if s.Token, err = c.login(username, password); err != nil {
			return err
		}
	default:
		return errors.New("the server offers no way to sign in")
	}
	if err := saveSettings(s); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Signed in to %s as %s.\n", server, username)
	return nil
}

// loginWithToken signs in with an API token made in the web client,
// asked for (or read from standard input) so it stays out of the shell's
// history.
func loginWithToken(c *client, in *bufio.Reader) error {
	fmt.Fprint(os.Stderr, "Token: ")
	var token string
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		token = string(b)
	} else {
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return err
		}
		token = line
	}
	c.Token = strings.TrimSpace(token)
	if !strings.HasPrefix(c.Token, "qst_") {
		return errors.New(`that isn't a Quicksand API token (they start with "qst_")`)
	}
	var u struct {
		Username string `json:"username"`
	}
	err := c.do("GET", "/api/auth/me", nil, &u)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == http.StatusUnauthorized {
		return errors.New("the server doesn't accept that token; it may have been revoked or expired")
	} else if err != nil {
		return err
	}
	if err := saveSettings(c.settings); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Signed in to %s as %s.\n", c.Server, u.Username)
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

func cmdLogout(ctx context.Context, cmd *cli.Command) error {
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

func cmdVersion(ctx context.Context, cmd *cli.Command) error {
	fmt.Println("qs", version.Version)
	c, err := signedIn()
	if err != nil {
		return nil
	}
	c.http.Timeout = 10 * time.Second
	var v struct {
		Version string `json:"version"`
	}
	if err := c.do("GET", "/api/version", nil, &v); err != nil {
		return fmt.Errorf("server %s: %w", c.Server, err)
	}
	fmt.Printf("server %s (%s)\n", v.Version, c.Server)
	return nil
}

func cmdWhoami(ctx context.Context, cmd *cli.Command) error {
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

func cmdList(ctx context.Context, cmd *cli.Command) error {
	c, err := signedIn()
	if err != nil {
		return err
	}
	path := "/api/vms"
	if cmd.Bool("a") {
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
	linked := slices.ContainsFunc(list, func(v vm) bool { return v.Link != nil })
	if linked {
		header += "\tLINKED"
	}
	if cmd.Bool("a") {
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
		if linked {
			link := "-"
			if v.Link != nil {
				link = v.Link.Name + " " + strings.Split(v.Link.Address, "/")[0]
				if v.Link.Status == "manual" {
					link += " (set manually)"
				}
			}
			line += "\t" + link
		}
		if cmd.Bool("a") {
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

func cmdTemplates(ctx context.Context, cmd *cli.Command) error {
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

func cmdNetworks(ctx context.Context, cmd *cli.Command) error {
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

func cmdDeploy(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 1 {
		return errors.New("usage: qs deploy [options] <template>")
	}
	c, err := signedIn()
	if err != nil {
		return err
	}
	req := map[string]any{
		"templateId": cmd.Args().First(), "name": cmd.String("name"),
		"cores": cmd.Int("cpus"), "memoryMiB": cmd.Int("memory"), "networkId": cmd.String("network"),
	}
	if cmd.Bool("no-agent") {
		req["agent"] = false
	}
	if u := cmd.String("url"); u != "" {
		req["url"] = u
	}
	var v vm
	if err := c.do("POST", "/api/vms", req, &v); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Deploying %s (VM %d).\n", v.Name, v.VMID)
	if cmd.Bool("no-wait") {
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

// findVM resolves a VM by Quicksand ID, Proxmox VM ID or name.
func findVM(c *client, ref string) (vm, error) {
	// Administrators can manage everyone's VMs; others get 403 here.
	var list []vm
	err := c.do("GET", "/api/vms?all=1", nil, &list)
	everyone := err == nil
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
		return vmOfTemplate(c, list, ref, everyone)
	}
	return vm{}, fmt.Errorf("%d VMs are named %q; use the VM ID instead", len(matches), ref)
}

// vmOfTemplate finds your one VM of a template, so that a template's ID
// ("win10") names it while you have just the one. everyone says the list
// holds everyone's VMs (an administrator's), of which only yours count.
func vmOfTemplate(c *client, list []vm, template string, everyone bool) (vm, error) {
	if everyone {
		var me struct {
			Username string `json:"username"`
		}
		if err := c.do("GET", "/api/auth/me", nil, &me); err != nil {
			return vm{}, err
		}
		list = slices.DeleteFunc(list, func(v vm) bool { return v.Owner != me.Username })
	}
	var names []string
	var match vm
	for _, v := range list {
		if v.TemplateID == template && v.State != "destroying" {
			names = append(names, v.Name)
			match = v
		}
	}
	switch len(names) {
	case 0:
		return vm{}, fmt.Errorf("no VM %q (see \"qs ls\")", template)
	case 1:
		return match, nil
	}
	return vm{}, fmt.Errorf("you have %d %s VMs (%s); name one", len(names), template, strings.Join(names, ", "))
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

func cmdPower(action string, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs "+action+" <vm>")
	if err != nil {
		return err
	}
	if err := c.do("POST", "/api/vms/"+url.PathEscape(v.ID)+"/power", map[string]string{"action": action}, nil); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Sent %s to %s.\n", action, v.Name)
	return nil
}

func cmdRelaunch(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs relaunch <vm>")
	if err != nil {
		return err
	}
	return c.do("POST", "/api/vms/"+url.PathEscape(v.ID)+"/relaunch", nil, nil)
}

func cmdExtend(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs extend <vm>")
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

func cmdDestroy(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs destroy [-y] <vm>")
	if err != nil {
		return err
	}
	if !cmd.Bool("y") {
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

func cmdPs(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs ps <vm>")
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

func cmdKill(ctx context.Context, cmd *cli.Command) error {
	if cmd.NArg() != 2 {
		return errors.New("usage: qs kill [-f] <vm> <pid>")
	}
	pid, err := strconv.Atoi(cmd.Args().Get(1))
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid pid %q", cmd.Args().Get(1))
	}
	c, v, err := vmArg(cmd.Args().Slice()[:1], "qs kill [-f] <vm> <pid>")
	if err != nil {
		return err
	}
	return c.do("POST", fmt.Sprintf("/api/vms/%s/processes/%d/kill", url.PathEscape(v.ID), pid),
		agentproto.KillRequest{Force: cmd.Bool("f")}, nil)
}

func cmdNetstat(ctx context.Context, cmd *cli.Command) error {
	c, v, err := vmArg(cmd.Args().Slice(), "qs netstat <vm>")
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
