//go:build windows

package browser

import (
	"context"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var shellExecuteW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")

func (Default) OpenBrowser(ctx context.Context, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateURL(value); err != nil {
		return err
	}
	operation, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return ErrInvalidURL
	}
	target, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return ErrInvalidURL
	}
	result, _, _ := shellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(operation)),
		uintptr(unsafe.Pointer(target)),
		0,
		0,
		1, // SW_SHOWNORMAL
	)
	if result <= 32 {
		return fmt.Errorf("default browser launch failed with code %d", result)
	}
	return ctx.Err()
}
