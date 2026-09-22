//go:build windows

package winui

import "syscall"

// Registry bindings used for autostart, theme detection and safe-setting reads.
var (
	advapi32              = syscall.NewLazyDLL("advapi32.dll")
	advapiRegOpenKeyEx    = advapi32.NewProc("RegOpenKeyExW")
	advapiRegQueryValueEx = advapi32.NewProc("RegQueryValueExW")
	advapiRegSetValueEx   = advapi32.NewProc("RegSetValueExW")
	advapiRegDeleteValue  = advapi32.NewProc("RegDeleteValueW")
	advapiRegCloseKey     = advapi32.NewProc("RegCloseKey")
	advapiRegCreateKeyEx  = advapi32.NewProc("RegCreateKeyExW")
)

// Registry root keys.
const (
	HKeyClassesRoot   = 0x80000000
	HKeyCurrentUser   = 0x80000001
	HKeyLocalMachine  = 0x80000002
	HKeyUsers         = 0x80000003
	HKeyCurrentConfig = 0x80000005
)

// Registry access rights.
const (
	KeyQueryValue   = 0x0001
	KeySetValue     = 0x0002
	KeyCreateSubKey = 0x0004
	KeyRead         = 0x20019
	KeyWrite        = 0x20006
	KeyAllAccess    = 0xF003F
)

// Registry value types.
const (
	RegSZ       = 1
	RegExpandSZ = 2
	RegDword    = 4
)
