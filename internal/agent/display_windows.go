package agent

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"quicksand/internal/agentproto"
)

// Resizing the display takes two steps, as the SPICE agent and virtio-win's
// viogpuap do: register the exact size as the driver's custom mode with a
// driver-private escape, then switch to it. Both have to happen in the
// console session (the login screen or the user's desktop): the agent's
// session 0 has no access to the display. So the agent runs a copy of
// itself there, as SYSTEM, for each change (RunDisplayHelper).

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	gdi32                        = windows.NewLazySystemDLL("gdi32.dll")
	procEnumDisplayDevicesW      = user32.NewProc("EnumDisplayDevicesW")
	procEnumDisplaySettingsW     = user32.NewProc("EnumDisplaySettingsW")
	procChangeDisplaySettingsExW = user32.NewProc("ChangeDisplaySettingsExW")
	procGetDisplayConfigBufSizes = user32.NewProc("GetDisplayConfigBufferSizes")
	procQueryDisplayConfig       = user32.NewProc("QueryDisplayConfig")
	procSetDisplayConfig         = user32.NewProc("SetDisplayConfig")
	procOpenInputDesktop         = user32.NewProc("OpenInputDesktop")
	procSetThreadDesktop         = user32.NewProc("SetThreadDesktop")
	procCreateDCW                = gdi32.NewProc("CreateDCW")
	procDeleteDC                 = gdi32.NewProc("DeleteDC")
	procD3DKMTOpenAdapterFromHdc = gdi32.NewProc("D3DKMTOpenAdapterFromHdc")
	procD3DKMTEscape             = gdi32.NewProc("D3DKMTEscape")
	procD3DKMTCloseAdapter       = gdi32.NewProc("D3DKMTCloseAdapter")
)

// displayHelperTimeout bounds a resolution change, mode switch included.
const displayHelperTimeout = 20 * time.Second

func setDisplay(ctx context.Context, width, height int) (agentproto.DisplayResult, error) {
	var res agentproto.DisplayResult
	exe, err := os.Executable()
	if err != nil {
		return res, err
	}
	out, err := runInConsoleSession(ctx, exe, "set-display", strconv.Itoa(width), strconv.Itoa(height))
	if err != nil {
		return res, err
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return res, fmt.Errorf("display helper: %w (%q)", err, out)
	}
	return res, nil
}

// runInConsoleSession runs exe with args (a helper command first) in the
// console session as SYSTEM and returns its output. A non-zero exit is an error carrying the output.
func runInConsoleSession(ctx context.Context, exe string, args ...string) ([]byte, error) {
	session := windows.WTSGetActiveConsoleSessionId()
	if session == 0xFFFFFFFF {
		return nil, errors.New("the guest has no console session")
	}
	var self windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, &self); err != nil {
		return nil, fmt.Errorf("open token: %w", err)
	}
	defer self.Close()
	var token windows.Token
	if err := windows.DuplicateTokenEx(self, windows.MAXIMUM_ALLOWED, nil, windows.SecurityIdentification, windows.TokenPrimary, &token); err != nil {
		return nil, fmt.Errorf("duplicate token: %w", err)
	}
	defer token.Close()
	// Moving a token to another session needs SeTcbPrivilege: the agent
	// must run as SYSTEM (as its service does).
	if err := windows.SetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), 4); err != nil {
		return nil, fmt.Errorf("move token to session %d: %w", session, err)
	}

	// One pipe for stdout and stderr; only its write end is inherited.
	var r, w windows.Handle
	sa := windows.SecurityAttributes{InheritHandle: 1}
	sa.Length = uint32(unsafe.Sizeof(sa))
	if err := windows.CreatePipe(&r, &w, &sa, 0); err != nil {
		return nil, err
	}
	windows.SetHandleInformation(r, windows.HANDLE_FLAG_INHERIT, 0)
	output := os.NewFile(uintptr(r), "helper")
	defer output.Close()

	app, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		windows.CloseHandle(w)
		return nil, err
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{exe}, args...)))
	if err != nil {
		windows.CloseHandle(w)
		return nil, err
	}
	// The interactive window station; the helper switches itself to
	// whichever desktop has the input (the login screen's, or the user's).
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Desktop: desktop, Flags: windows.STARTF_USESTDHANDLES, StdOutput: w, StdErr: w}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	err = windows.CreateProcessAsUser(token, app, cmdLine, nil, nil, true, windows.CREATE_NO_WINDOW, nil, nil, &si, &pi)
	windows.CloseHandle(w)
	if err != nil {
		return nil, fmt.Errorf("start the %s helper: %w", args[0], err)
	}
	defer windows.CloseHandle(pi.Process)
	windows.CloseHandle(pi.Thread)

	read := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(output, 4<<20))
		read <- b
	}()
	timeout := displayHelperTimeout
	if d, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(d))
	}
	if ev, _ := windows.WaitForSingleObject(pi.Process, uint32(timeout.Milliseconds())); ev != windows.WAIT_OBJECT_0 {
		windows.TerminateProcess(pi.Process, 1)
		return nil, fmt.Errorf("the %s helper timed out", args[0])
	}
	var code uint32
	windows.GetExitCodeProcess(pi.Process, &code)
	out := <-read
	if code != 0 {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = fmt.Sprintf("the %s helper failed (exit code %#x)", args[0], code)
		}
		return nil, errors.New(msg)
	}
	return out, nil
}

// RunDisplayHelper is the "set-display WIDTH HEIGHT" command the agent
// runs in the console session. It prints an agentproto.DisplayResult.
func RunDisplayHelper(args []string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(args) != 2 {
		return fail(errors.New("usage: set-display WIDTH HEIGHT"))
	}
	width, err1 := strconv.Atoi(args[0])
	height, err2 := strconv.Atoi(args[1])
	if err1 != nil || err2 != nil || width <= 0 || height <= 0 {
		return fail(errors.New("invalid size"))
	}
	// Display changes need the thread on the input desktop.
	runtime.LockOSThread()
	if desk, _, _ := procOpenInputDesktop.Call(0, 0, windows.GENERIC_ALL); desk != 0 {
		procSetThreadDesktop.Call(desk)
	}
	res, err := applyDisplay(width, height)
	if err != nil {
		return fail(err)
	}
	json.NewEncoder(os.Stdout).Encode(res)
	return 0
}

// displayDevice is DISPLAY_DEVICEW.
type displayDevice struct {
	Cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

// devMode is DEVMODEW, as used for displays.
type devMode struct {
	DeviceName       [32]uint16
	SpecVersion      uint16
	DriverVersion    uint16
	Size             uint16
	DriverExtra      uint16
	Fields           uint32
	Position         [16]byte // POINTL position, orientation, fixed output
	Color            int16
	Duplex           int16
	YResolution      int16
	TTOption         int16
	Collate          int16
	FormName         [32]uint16
	LogPixels        uint16
	BitsPerPel       uint32
	PelsWidth        uint32
	PelsHeight       uint32
	DisplayFlags     uint32
	DisplayFrequency uint32
	ICMMethod        uint32
	ICMIntent        uint32
	MediaType        uint32
	DitherType       uint32
	Reserved1        uint32
	Reserved2        uint32
	PanningWidth     uint32
	PanningHeight    uint32
}

// d3dkmtOpenAdapterFromHDC is D3DKMT_OPENADAPTERFROMHDC.
type d3dkmtOpenAdapterFromHDC struct {
	HDC           uintptr
	Adapter       uint32
	LUIDLow       uint32
	LUIDHigh      int32
	VidPnSourceID uint32
}

// d3dkmtEscape is D3DKMT_ESCAPE; Type 0 is D3DKMT_ESCAPE_DRIVERPRIVATE.
type d3dkmtEscape struct {
	Adapter  uint32
	Device   uint32
	Type     uint32
	Flags    uint32
	Data     unsafe.Pointer
	DataSize uint32
	Context  uint32
}

// The structs must match the Windows (64-bit) layouts exactly.
var (
	_ = [1]struct{}{}[unsafe.Sizeof(displayDevice{})-840]
	_ = [1]struct{}{}[unsafe.Sizeof(devMode{})-220]
	_ = [1]struct{}{}[unsafe.Sizeof(d3dkmtOpenAdapterFromHDC{})-24]
	_ = [1]struct{}{}[unsafe.Sizeof(d3dkmtEscape{})-32]
)

const (
	displayAttachedToDesktop = 0x1
	displayPrimaryDevice     = 0x4
	enumCurrentSettings      = 0xFFFFFFFF
	dmBitsPerPel             = 0x40000
	dmPelsWidth              = 0x80000
	dmPelsHeight             = 0x100000
	cdsUpdateRegistry        = 0x1
	cdsGlobal                = 0x8
)

func applyDisplay(width, height int) (agentproto.DisplayResult, error) {
	res := agentproto.DisplayResult{}
	dev, err := primaryDisplay()
	if err != nil {
		return res, err
	}
	name := windows.UTF16ToString(dev.DeviceName[:])
	// The driver, not the device: Windows drives a QXL or VirtIO GPU
	// device with its basic display driver until the right one is
	// installed.
	driver := windows.UTF16ToString(dev.DeviceString[:])
	switch upper := strings.ToUpper(driver); {
	case strings.Contains(upper, "QXL"):
		res.Adapter = "qxl" // qxldod (spice-guest-tools)
	case strings.Contains(upper, "VIRTIO GPU"):
		res.Adapter = "virtio-gpu" // viogpudo (virtio-win)
	default:
		res.Adapter = driver
	}
	if res.Adapter == "qxl" || res.Adapter == "virtio-gpu" {
		err := setCustomMode(name, res.Adapter, width, height)
		var refused escapeError
		switch {
		case err == nil:
			res.Exact = true
		case res.Adapter == "virtio-gpu" && errors.As(err, &refused) && refused == statusInvalidParameter:
			// virtio-win drivers before mid-2026 can't take custom sizes.
		default:
			return res, err
		}
	}
	if !res.Exact {
		// Use the closest mode the driver offers and let the client scale.
		if width, height, err = closestMode(name, width, height); err != nil {
			return res, err
		}
	}
	// A custom mode isn't in Windows' cached mode list, so it has to be
	// committed as a new display configuration (as viogpuap does).
	if res.Exact {
		err = setSourceMode(width, height)
	} else {
		err = changeMode(name, width, height)
	}
	if err != nil {
		return res, err
	}
	res.Width, res.Height = width, height
	if cur, err := currentMode(name); err == nil {
		res.Width, res.Height = int(cur.PelsWidth), int(cur.PelsHeight)
	}
	return res, nil
}

func primaryDisplay() (displayDevice, error) {
	var first *displayDevice
	for i := uint32(0); ; i++ {
		var d displayDevice
		d.Cb = uint32(unsafe.Sizeof(d))
		if ok, _, _ := procEnumDisplayDevicesW.Call(0, uintptr(i), uintptr(unsafe.Pointer(&d)), 0); ok == 0 {
			break
		}
		if d.StateFlags&displayPrimaryDevice != 0 {
			return d, nil
		}
		if first == nil && d.StateFlags&displayAttachedToDesktop != 0 {
			first = &d
		}
	}
	if first == nil {
		return displayDevice{}, errors.New("no display is attached")
	}
	return *first, nil
}

// setCustomMode registers width×height as the driver's custom mode.
func setCustomMode(device, adapter string, width, height int) error {
	name, err := windows.UTF16PtrFromString(device)
	if err != nil {
		return err
	}
	hdc, _, err := procCreateDCW.Call(0, uintptr(unsafe.Pointer(name)), 0, 0)
	if hdc == 0 {
		return fmt.Errorf("open display %s: %w", device, err)
	}
	defer procDeleteDC.Call(hdc)
	open := d3dkmtOpenAdapterFromHDC{HDC: hdc}
	if st, _, _ := procD3DKMTOpenAdapterFromHdc.Call(uintptr(unsafe.Pointer(&open))); st != 0 {
		return fmt.Errorf("open the display adapter: NTSTATUS %#x", uint32(st))
	}
	defer func() {
		closeAdapter := struct{ Adapter uint32 }{open.Adapter}
		procD3DKMTCloseAdapter.Call(uintptr(unsafe.Pointer(&closeAdapter)))
	}()

	var data []byte
	switch adapter {
	case "virtio-gpu":
		// VIOGPU_ESCAPE (viogpum.h, packed): Type, DataLength, then the
		// VIOGPU_DISP_MODE; VIOGPU_SET_CUSTOM_RESOLUTION is 2.
		data = make([]byte, 8)
		binary.LittleEndian.PutUint16(data[0:], 2)
		binary.LittleEndian.PutUint16(data[2:], 4)
		binary.LittleEndian.PutUint16(data[4:], uint16(width))
		binary.LittleEndian.PutUint16(data[6:], uint16(height))
	case "qxl":
		// The ioctl, then QXLEscapeSetCustomDisplay (qxl_windows.h):
		// QXL_ESCAPE_SET_CUSTOM_DISPLAY is 0x10001.
		data = make([]byte, 16)
		binary.LittleEndian.PutUint32(data[0:], 0x10001)
		binary.LittleEndian.PutUint32(data[4:], uint32(width))
		binary.LittleEndian.PutUint32(data[8:], uint32(height))
		binary.LittleEndian.PutUint32(data[12:], 32)
	}
	esc := d3dkmtEscape{Adapter: open.Adapter, Data: unsafe.Pointer(&data[0]), DataSize: uint32(len(data))}
	st, _, _ := procD3DKMTEscape.Call(uintptr(unsafe.Pointer(&esc)))
	runtime.KeepAlive(data)
	if st != 0 {
		return fmt.Errorf("the %s driver refused %d×%d (it may be too large for the display memory): %w",
			adapter, width, height, escapeError(st))
	}
	return nil
}

// escapeError is the NTSTATUS a driver escape failed with.
type escapeError uint32

const statusInvalidParameter escapeError = 0xC000000D

func (e escapeError) Error() string { return fmt.Sprintf("NTSTATUS %#x", uint32(e)) }

// closestMode picks the display's largest mode that fits in width×height,
// or its smallest if none does.
func closestMode(device string, width, height int) (int, int, error) {
	name, err := windows.UTF16PtrFromString(device)
	if err != nil {
		return 0, 0, err
	}
	bestW, bestH, smallW, smallH := 0, 0, 0, 0
	for i := uintptr(0); ; i++ {
		var dm devMode
		dm.Size = uint16(unsafe.Sizeof(dm))
		if ok, _, _ := procEnumDisplaySettingsW.Call(uintptr(unsafe.Pointer(name)), i, uintptr(unsafe.Pointer(&dm))); ok == 0 {
			break
		}
		w, h := int(dm.PelsWidth), int(dm.PelsHeight)
		if dm.BitsPerPel != 32 || w == 0 || h == 0 {
			continue
		}
		if w <= width && h <= height && w*h > bestW*bestH {
			bestW, bestH = w, h
		}
		if smallW == 0 || w*h < smallW*smallH {
			smallW, smallH = w, h
		}
	}
	switch {
	case bestW > 0:
		return bestW, bestH, nil
	case smallW > 0:
		return smallW, smallH, nil
	}
	return 0, 0, errors.New("the display offers no modes")
}

// The display configuration (CCD) API's records, handled as raw bytes:
// DISPLAYCONFIG_PATH_INFO is 72 bytes, DISPLAYCONFIG_MODE_INFO 64, with
// infoType at 0 and, for source modes, width and height at 16 and 20.
const (
	pathInfoSize         = 72
	modeInfoSize         = 64
	modeInfoTypeSource   = 1
	qdcOnlyActivePaths   = 0x2
	sdcUseSuppliedConfig = 0x20
	sdcApply             = 0x80
	sdcSaveToDatabase    = 0x200
	sdcAllowChanges      = 0x400
)

// setSourceMode sets the active display's desktop size to width×height.
func setSourceMode(width, height int) error {
	var nPaths, nModes uint32
	if r, _, _ := procGetDisplayConfigBufSizes.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&nPaths)), uintptr(unsafe.Pointer(&nModes))); r != 0 {
		return fmt.Errorf("query the display configuration: %w", windows.Errno(r))
	}
	if nPaths == 0 || nModes == 0 {
		return errors.New("no active display")
	}
	paths := make([]byte, nPaths*pathInfoSize)
	modes := make([]byte, nModes*modeInfoSize)
	if r, _, _ := procQueryDisplayConfig.Call(qdcOnlyActivePaths, uintptr(unsafe.Pointer(&nPaths)), uintptr(unsafe.Pointer(&paths[0])),
		uintptr(unsafe.Pointer(&nModes)), uintptr(unsafe.Pointer(&modes[0])), 0); r != 0 {
		return fmt.Errorf("query the display configuration: %w", windows.Errno(r))
	}
	found := false
	for i := range int(nModes) {
		m := modes[i*modeInfoSize : (i+1)*modeInfoSize]
		if binary.LittleEndian.Uint32(m) == modeInfoTypeSource {
			binary.LittleEndian.PutUint32(m[16:], uint32(width))
			binary.LittleEndian.PutUint32(m[20:], uint32(height))
			found = true
		}
	}
	if !found {
		return errors.New("the display configuration has no source mode")
	}
	r, _, _ := procSetDisplayConfig.Call(uintptr(nPaths), uintptr(unsafe.Pointer(&paths[0])), uintptr(nModes), uintptr(unsafe.Pointer(&modes[0])),
		sdcApply|sdcUseSuppliedConfig|sdcAllowChanges|sdcSaveToDatabase)
	if r != 0 {
		return fmt.Errorf("apply %d×%d: %w", width, height, windows.Errno(r))
	}
	return nil
}

func changeMode(device string, width, height int) error {
	name, err := windows.UTF16PtrFromString(device)
	if err != nil {
		return err
	}
	dm := devMode{
		Fields:     dmPelsWidth | dmPelsHeight | dmBitsPerPel,
		BitsPerPel: 32,
		PelsWidth:  uint32(width),
		PelsHeight: uint32(height),
	}
	dm.Size = uint16(unsafe.Sizeof(dm))
	r, _, _ := procChangeDisplaySettingsExW.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&dm)), 0,
		cdsUpdateRegistry|cdsGlobal, 0)
	switch int32(r) {
	case 0: // DISP_CHANGE_SUCCESSFUL
		return nil
	case 1:
		return errors.New("the new resolution needs a restart")
	case -2:
		return fmt.Errorf("the display doesn't offer %d×%d", width, height)
	}
	return fmt.Errorf("changing the resolution failed (%d)", int32(r))
}

func currentMode(device string) (devMode, error) {
	name, err := windows.UTF16PtrFromString(device)
	if err != nil {
		return devMode{}, err
	}
	var dm devMode
	dm.Size = uint16(unsafe.Sizeof(dm))
	if ok, _, err := procEnumDisplaySettingsW.Call(uintptr(unsafe.Pointer(name)), enumCurrentSettings, uintptr(unsafe.Pointer(&dm))); ok == 0 {
		return dm, err
	}
	return dm, nil
}
