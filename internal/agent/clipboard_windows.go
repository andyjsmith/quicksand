package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The clipboard belongs to the console session's window station, which
// the agent's session 0 can't reach: like display changes, the agent runs
// a copy of itself there for each read or write (RunClipboardHelper).

var (
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procEmptyClipboard             = user32.NewProc("EmptyClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procSetClipboardData           = user32.NewProc("SetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procCreateWindowExW            = user32.NewProc("CreateWindowExW")
	procDestroyWindow              = user32.NewProc("DestroyWindow")
	procGlobalAlloc                = kernel32.NewProc("GlobalAlloc")
	procGlobalFree                 = kernel32.NewProc("GlobalFree")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
)

const (
	cfUnicodeText = 13
	gmemMoveable  = 0x0002
	hwndMessage   = ^uintptr(2) // HWND_MESSAGE, (HWND)-3
)

func readClipboard(ctx context.Context) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := runInConsoleSession(ctx, exe, "clipboard-get")
	if err != nil {
		return "", clipboardSessionError(err)
	}
	var res struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return "", fmt.Errorf("clipboard helper: %w", err)
	}
	return res.Text, nil
}

func writeClipboard(ctx context.Context, text string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// The text goes through a file: command lines are short.
	f, err := os.CreateTemp("", "quicksand-clipboard-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if _, err := runInConsoleSession(ctx, exe, "clipboard-set", f.Name()); err != nil {
		return clipboardSessionError(err)
	}
	return nil
}

func clipboardSessionError(err error) error {
	if strings.Contains(err.Error(), "no console session") {
		return fmt.Errorf("%w: %v", errClipboardUnsupported, err)
	}
	return err
}

// RunClipboardHelper is the "clipboard-get" or "clipboard-set FILE"
// command the agent runs in the console session. clipboard-get prints
// {"text": ...}; clipboard-set puts the file's text on the clipboard.
func RunClipboardHelper(args []string) int {
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switch {
	case len(args) == 1 && args[0] == "clipboard-get":
		text, err := getClipboardText()
		if err != nil {
			return fail(err)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]string{"text": text})
	case len(args) == 2 && args[0] == "clipboard-set":
		data, err := os.ReadFile(args[1])
		if err != nil {
			return fail(err)
		}
		if err := setClipboardText(string(data)); err != nil {
			return fail(err)
		}
	default:
		return fail(errors.New("usage: clipboard-get | clipboard-set FILE"))
	}
	return 0
}

// openClipboard opens the clipboard for window, retrying while another
// program has it open.
func openClipboard(window uintptr) error {
	var err error
	for i := 0; i < 20; i++ {
		var r uintptr
		if r, _, err = procOpenClipboard.Call(window); r != 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("open the clipboard: %w", err)
}

func getClipboardText() (string, error) {
	if err := openClipboard(0); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()
	if r, _, _ := procIsClipboardFormatAvailable.Call(cfUnicodeText); r == 0 {
		return "", nil
	}
	h, _, err := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", fmt.Errorf("read the clipboard: %w", err)
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		return "", fmt.Errorf("read the clipboard: %w", err)
	}
	defer procGlobalUnlock.Call(h)
	// Plain newlines, as everywhere else: browsers convert them for the host.
	return strings.ReplaceAll(windows.UTF16PtrToString((*uint16)(unsafe.Add(nil, p))), "\r\n", "\n"), nil
}

func setClipboardText(text string) error {
	// Text with NULs would be cut short anyway.
	text = strings.ReplaceAll(text, "\x00", "")
	// Windows programs expect CRLF line endings on the clipboard.
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
	u, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	size := uintptr(len(u)) * 2
	h, _, err := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("allocate: %w", err)
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("lock: %w", err)
	}
	copy(unsafe.Slice((*uint16)(unsafe.Add(nil, p)), len(u)), u)
	procGlobalUnlock.Call(h)

	// Emptying the clipboard makes the opening window its owner; with no
	// window, setting data fails. A message-only window will do.
	class, _ := windows.UTF16PtrFromString("STATIC")
	window, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if window == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("create a window: %w", err)
	}
	defer procDestroyWindow.Call(window)
	if err := openClipboard(window); err != nil {
		procGlobalFree.Call(h)
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	if r, _, err := procSetClipboardData.Call(cfUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("set the clipboard: %w", err)
	}
	// The clipboard owns the memory now.
	return nil
}
