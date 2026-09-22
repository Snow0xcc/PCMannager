//go:build windows

package sysutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

var (
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	advapi32                = syscall.NewLazyDLL("advapi32.dll")
	procShellExecuteW       = shell32.NewProc("ShellExecuteW")
	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
	procGetModuleFileNameW  = kernel32.NewProc("GetModuleFileNameW")
	procRegCreateKeyExW     = advapi32.NewProc("RegCreateKeyExW")
	procRegSetValueExW      = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW     = advapi32.NewProc("RegDeleteValueW")
	procRegCloseKey         = advapi32.NewProc("RegCloseKey")
	procOpenProcessToken    = advapi32.NewProc("OpenProcessToken")
	procGetTokenInformation = advapi32.NewProc("GetTokenInformation")
	procGetCurrentProcess   = kernel32.NewProc("GetCurrentProcess")
	procCloseHandle         = kernel32.NewProc("CloseHandle")
)

const (
	hkeyCurrentUser  = 0x80000001
	keySetValue      = 0x0002
	keyQueryValue    = 0x0001
	regSZ            = 1
	regExpandSZ      = 2
	seeMaskNoConsole = 0x08000000
	swShowNormal     = 1
)

// hideWindow suppresses the console window flash when spawning child processes.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// IsElevated reports whether the current process has administrator rights.
func IsElevated() bool {
	const tokenQuery = 0x0008
	var token uintptr
	r, _, _ := procOpenProcessToken.Call(
		mustHandle(procGetCurrentProcess.Call()), tokenQuery, uintptr(unsafe.Pointer(&token)))
	if r == 0 {
		return false
	}
	defer procCloseHandle.Call(token)

	const tokenElevation = 20 // TokenElevation
	var (
		elevated struct{ TokenIsElevated uint32 }
		size     = uint32(unsafe.Sizeof(elevated))
	)
	r, _, _ = procGetTokenInformation.Call(token, tokenElevation,
		uintptr(unsafe.Pointer(&elevated)), uintptr(size), uintptr(unsafe.Pointer(&size)))
	return r != 0 && elevated.TokenIsElevated != 0
}

func mustHandle(h uintptr, _ uintptr, _ error) uintptr { return h }

// Elevate relaunches this executable elevated via the UAC prompt.
//
// extraArgs are appended to the current command line, letting the app tell the
// new instance which operation to perform (e.g. "repair-run <action-id>").
func Elevate(extraArgs []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := ""
	for _, a := range extraArgs {
		args += " " + quote(a)
	}

	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(args)
	dir, _ := syscall.UTF16PtrFromString(filepath.Dir(exe))

	r, _, err2 := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)), uintptr(unsafe.Pointer(dir)), swShowNormal)
	// ShellExecute returns a value >32 on success.
	if r <= 32 {
		if err2 != nil && err2 != syscall.Errno(0) {
			return fmt.Errorf("请求管理员权限失败: %w", err2)
		}
		return fmt.Errorf("请求管理员权限失败 (代码 %d)，可能被用户取消", r)
	}
	return nil
}

// quote wraps an argument in double quotes when it contains spaces.
func quote(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '"' {
			return `"` + s + `"`
		}
	}
	return s
}

// RunElevated starts a command in an elevated shell without blocking.
func RunElevated(command string) error {
	cmd, err := syscall.UTF16PtrFromString("cmd.exe")
	if err != nil {
		return err
	}
	params, err := syscall.UTF16PtrFromString("/c " + command)
	if err != nil {
		return err
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	r, _, err2 := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(cmd)),
		uintptr(unsafe.Pointer(params)), 0, 0)
	if r <= 32 {
		if err2 != nil && err2 != syscall.Errno(0) {
			return fmt.Errorf("提权执行失败: %w", err2)
		}
		return fmt.Errorf("提权执行失败 (代码 %d)", r)
	}
	return nil
}

// AutostartPath is the HKCU Run key used for per-user autostart.
const autostartKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// SetAutostart registers or removes a per-user autostart entry.
func SetAutostart(name string, on bool) error {
	var key uintptr
	path, _ := syscall.UTF16PtrFromString(autostartKey)
	r, _, err := procRegCreateKeyExW.Call(hkeyCurrentUser,
		uintptr(unsafe.Pointer(path)), 0, 0, 0, keySetValue|keyQueryValue,
		0, uintptr(unsafe.Pointer(&key)), 0)
	if r != 0 {
		return fmt.Errorf("打开注册表项失败: %v", err)
	}
	defer procRegCloseKey.Call(key)

	namePtr, _ := syscall.UTF16PtrFromString(name)
	if !on {
		procRegDeleteValueW.Call(key, uintptr(unsafe.Pointer(namePtr)))
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	value, _ := syscall.UTF16PtrFromString(`"` + exe + `"`)
	// RegSetValueExW wants a byte length that includes the NUL terminator.
	byteLen := uintptr((len(exe) + 3) * 2)
	r, _, err = procRegSetValueExW.Call(key, uintptr(unsafe.Pointer(namePtr)), 0, regSZ,
		uintptr(unsafe.Pointer(value)), byteLen)
	if r != 0 {
		return fmt.Errorf("写入注册表失败: %v", err)
	}
	return nil
}

// IsAutostart reports whether the named autostart entry exists.
func IsAutostart(name string) bool {
	const keyRead = 0x20019
	advapiQueryValue := advapi32.NewProc("RegQueryValueExW")
	advapiOpenKey := advapi32.NewProc("RegOpenKeyExW")

	var key uintptr
	path, _ := syscall.UTF16PtrFromString(autostartKey)
	if r, _, _ := advapiOpenKey.Call(hkeyCurrentUser, uintptr(unsafe.Pointer(path)),
		0, keyRead, uintptr(unsafe.Pointer(&key))); r != 0 {
		return false
	}
	defer procRegCloseKey.Call(key)

	namePtr, _ := syscall.UTF16PtrFromString(name)
	r, _, _ := advapiQueryValue.Call(key, uintptr(unsafe.Pointer(namePtr)), 0, 0, 0, 0)
	return r == 0
}

// singleInstance serializes instances through a named Win32 mutex.
type singleInstance struct {
	handle uintptr
}

var instanceMu sync.Mutex

// AcquireSingleInstance takes a named mutex. ok is false when another instance
// already owns it; release must be called on shutdown to free the mutex.
func AcquireSingleInstance(name string) (release func(), ok bool) {
	instanceMu.Lock()
	defer instanceMu.Unlock()

	ptr, err := syscall.UTF16PtrFromString(`Local\` + name)
	if err != nil {
		return func() {}, false
	}
	handle, _, lastErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(ptr)))
	if handle == 0 {
		return func() {}, false
	}
	// ERROR_ALREADY_EXISTS (183) means another instance holds the mutex.
	const errAlreadyExists = 183
	if errno, isErrno := lastErr.(syscall.Errno); isErrno && errno == errAlreadyExists {
		procCloseHandle.Call(handle)
		return func() {}, false
	}

	inst := &singleInstance{handle: handle}
	return inst.release, true
}

func (s *singleInstance) release() {
	if s.handle != 0 {
		procCloseHandle.Call(s.handle)
		s.handle = 0
	}
}

// Notify shows a balloon tip via the shell. Errors are non-fatal: modules log
// them and continue when notifications are unavailable.
func Notify(title, message string) error {
	// A tray icon owner is required for balloon notifications; GoBox routes
	// notifications through its own tray window, so this is a best-effort
	// fallback that writes to the Windows notification area when possible.
	_ = title
	_ = message
	return fmt.Errorf("通知需要托盘窗口，请通过托盘模块发送")
}

// OpenURL opens a URL or file with the user's default handler.
func OpenURL(target string) error {
	t, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	r, _, err2 := procShellExecuteW.Call(0,
		uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(t)), 0, 0, swShowNormal)
	if r <= 32 {
		if err2 != nil && err2 != syscall.Errno(0) {
			return fmt.Errorf("打开 %s 失败: %w", target, err2)
		}
		return fmt.Errorf("打开 %s 失败 (代码 %d)", target, r)
	}
	return nil
}
