package agent

import (
	"errors"
	"os"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"quicksand/internal/agentproto"
)

var (
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procGetProcessMemoryInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

// processMemoryCounters is PROCESS_MEMORY_COUNTERS.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func listProcesses() ([]agentproto.Process, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)

	users := map[string]string{}
	out := []agentproto.Process{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		p := agentproto.Process{
			PID:     int(entry.ProcessID),
			PPID:    int(entry.ParentProcessID),
			Name:    windows.UTF16ToString(entry.ExeFile[:]),
			Threads: int(entry.Threads),
		}
		if p.PID != 0 {
			describeProcess(&p, users)
		}
		out = append(out, p)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	services := runningServices()
	for i := range out {
		out[i].Services = services[out[i].PID]
	}
	return out, nil
}

// runningServices maps process IDs to the services running in them, from
// the service control manager; nil if it can't be read.
func runningServices() map[int][]agentproto.ProcessService {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil
	}
	defer windows.CloseServiceHandle(m)
	out := map[int][]agentproto.ProcessService{}
	buf := make([]byte, 64<<10)
	var resume uint32
	for {
		var needed, count uint32
		err := windows.EnumServicesStatusEx(m, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32, windows.SERVICE_ACTIVE,
			&buf[0], uint32(len(buf)), &needed, &count, &resume, nil)
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return out
		}
		// The names point into buf: copy them before it's reused.
		for _, e := range unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), count) {
			if pid := int(e.ServiceStatusProcess.ProcessId); pid != 0 {
				out[pid] = append(out[pid], agentproto.ProcessService{
					Name:        windows.UTF16PtrToString(e.ServiceName),
					DisplayName: windows.UTF16PtrToString(e.DisplayName),
				})
			}
		}
		if err == nil {
			break
		}
		if int(needed) > len(buf) {
			buf = make([]byte, needed)
		}
	}
	for _, svcs := range out {
		slices.SortFunc(svcs, func(a, b agentproto.ProcessService) int { return strings.Compare(a.Name, b.Name) })
	}
	return out
}

// describeProcess fills in what needs a process handle; protected
// processes keep only their snapshot fields.
func describeProcess(p *agentproto.Process, users map[string]string) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(p.PID))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)

	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &creation, &exit, &kernel, &user) == nil {
		// Filetimes count 100 ns intervals.
		ticks := filetimeTicks(kernel) + filetimeTicks(user)
		p.CPUTimeMs = int64(ticks / 10000)
		p.Started = time.Unix(0, creation.Nanoseconds()).UTC()
	}
	mem := processMemoryCounters{CB: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mem)), uintptr(mem.CB)); r != 0 {
		p.Memory = uint64(mem.WorkingSetSize)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) == nil {
		p.Command = windows.UTF16ToString(buf[:n])
	}
	var token windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token) == nil {
		defer token.Close()
		if tu, err := token.GetTokenUser(); err == nil {
			sid := tu.User.Sid.String()
			name, ok := users[sid]
			if !ok {
				name = sid
				if account, domain, _, err := tu.User.Sid.LookupAccount(""); err == nil {
					name = domain + `\` + account
				}
				users[sid] = name
			}
			p.User = name
		}
	}
}

func memoryTotal() uint64 {
	st := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	if r, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&st))); r == 0 {
		return 0
	}
	return st.TotalPhys
}

func killProcess(pid int, force bool) error {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	switch {
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		return os.ErrProcessDone
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return os.ErrPermission
	case err != nil:
		return err
	}
	defer windows.CloseHandle(h)
	return windows.TerminateProcess(h, 1)
}

func filetimeTicks(ft windows.Filetime) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}
