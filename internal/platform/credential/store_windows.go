//go:build windows

package credential

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	credentialTypeGeneric         = 1
	credentialPersistLocalMachine = 2
)

var (
	advapi32       = windows.NewLazySystemDLL("advapi32.dll")
	procCredWrite  = advapi32.NewProc("CredWriteW")
	procCredRead   = advapi32.NewProc("CredReadW")
	procCredDelete = advapi32.NewProc("CredDeleteW")
	procCredFree   = advapi32.NewProc("CredFree")
)

type nativeCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

type windowsNativeAPI struct{}

func NewWindowsStore() *WindowsStore { return newWindowsStore(windowsNativeAPI{}) }

func NewDefaultStore() *WindowsStore { return NewWindowsStore() }

func (windowsNativeAPI) Put(target string, value []byte) error {
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	credential := nativeCredential{
		Type:               credentialTypeGeneric,
		TargetName:         targetUTF16,
		CredentialBlobSize: uint32(len(value)),
		CredentialBlob:     &value[0],
		Persist:            credentialPersistLocalMachine,
	}
	result, _, callErr := procCredWrite.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if result == 0 {
		return callErr
	}
	return nil
}

func (windowsNativeAPI) Get(target string) ([]byte, error) {
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, err
	}
	var credential *nativeCredential
	result, _, callErr := procCredRead.Call(uintptr(unsafe.Pointer(targetUTF16)), credentialTypeGeneric, 0, uintptr(unsafe.Pointer(&credential)))
	if result == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil, errWindowsCredentialNotFound
		}
		return nil, callErr
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential == nil || credential.CredentialBlobSize == 0 || credential.CredentialBlob == nil {
		return nil, errWindowsCredentialNotFound
	}
	value := append([]byte(nil), unsafe.Slice(credential.CredentialBlob, int(credential.CredentialBlobSize))...)
	return value, nil
}

func (windowsNativeAPI) Delete(target string) error {
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := procCredDelete.Call(uintptr(unsafe.Pointer(targetUTF16)), credentialTypeGeneric, 0)
	if result == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return errWindowsCredentialNotFound
		}
		return callErr
	}
	return nil
}
