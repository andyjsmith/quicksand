package agent

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"
	"time"

	"quicksand/internal/agentproto"
)

// clockTicks is USER_HZ, which is 100 on every Linux architecture Go
// supports.
const clockTicks = 100

func listProcesses() ([]agentproto.Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	boot := bootTime()
	page := uint64(os.Getpagesize())
	users := map[string]string{}
	out := []agentproto.Process{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		p, ok := readProcess(pid, boot, page, users)
		if ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func readProcess(pid int, boot time.Time, page uint64, users map[string]string) (agentproto.Process, bool) {
	dir := "/proc/" + strconv.Itoa(pid) + "/"
	stat, err := os.ReadFile(dir + "stat")
	if err != nil {
		return agentproto.Process{}, false
	}
	// pid (comm) state ppid ...; comm may contain spaces and parentheses.
	open, close := bytes.IndexByte(stat, '('), bytes.LastIndexByte(stat, ')')
	if open < 0 || close < open {
		return agentproto.Process{}, false
	}
	p := agentproto.Process{PID: pid, Name: string(stat[open+1 : close])}
	f := strings.Fields(string(stat[close+1:]))
	if len(f) < 22 {
		return agentproto.Process{}, false
	}
	num := func(i int) uint64 { n, _ := strconv.ParseUint(f[i], 10, 64); return n }
	p.PPID = int(num(1))
	p.CPUTimeMs = int64((num(11) + num(12)) * 1000 / clockTicks)
	p.Threads = int(num(17))
	if !boot.IsZero() {
		p.Started = boot.Add(time.Duration(num(19)) * time.Second / clockTicks).UTC()
	}
	p.Memory = num(21) * page

	if cmdline, err := os.ReadFile(dir + "cmdline"); err == nil {
		p.Command = strings.TrimSpace(string(bytes.ReplaceAll(cmdline, []byte{0}, []byte{' '})))
	}
	if unit := systemdService(dir); unit != "" {
		p.Services = []agentproto.ProcessService{{Name: unit}}
	}
	if status, err := os.ReadFile(dir + "status"); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(status))
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "Uid:"); ok {
				if fields := strings.Fields(rest); len(fields) > 0 {
					p.User = userName(fields[0], users)
				}
				break
			}
		}
	}
	return p, true
}

// systemdService is the systemd service unit a process runs in, from its
// cgroup (e.g. "0::/system.slice/ssh.service"); "" if none.
func systemdService(dir string) string {
	data, err := os.ReadFile(dir + "cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		// The unified hierarchy's line is "0::/path".
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			if unit := path[strings.LastIndexByte(path, '/')+1:]; strings.HasSuffix(unit, ".service") {
				return unit
			}
		}
	}
	return ""
}

func userName(uid string, cache map[string]string) string {
	if name, ok := cache[uid]; ok {
		return name
	}
	name := uid
	if u, err := user.LookupId(uid); err == nil {
		name = u.Username
	}
	cache[uid] = name
	return name
}

func bootTime() time.Time {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}
	}
	for line := range strings.SplitSeq(string(stat), "\n") {
		if rest, ok := strings.CutPrefix(line, "btime "); ok {
			if secs, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64); err == nil {
				return time.Unix(secs, 0)
			}
		}
	}
	return time.Time{}
}

func memoryTotal() uint64 {
	var info syscall.Sysinfo_t
	if syscall.Sysinfo(&info) != nil {
		return 0
	}
	return uint64(info.Totalram) * uint64(info.Unit)
}

func killProcess(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	err := syscall.Kill(pid, sig)
	switch {
	case errors.Is(err, syscall.ESRCH):
		return os.ErrProcessDone
	case errors.Is(err, syscall.EPERM):
		return os.ErrPermission
	}
	return err
}
