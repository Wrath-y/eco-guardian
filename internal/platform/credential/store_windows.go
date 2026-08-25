//go:build windows

package credential

import (
	"context"
	"errors"
	"unsafe"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
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

type WindowsStore struct{}

func NewWindowsStore() *WindowsStore { return &WindowsStore{} }

func (*WindowsStore) Put(_ context.Context, provider string, value []byte) error {
	target, err := targetName(provider)
	if err != nil || len(value) == 0 || len(value) > aiprovider.MaxCredentialBytes {
		return aiprovider.ErrCredentialInvalid
	}
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return aiprovider.ErrCredentialInvalid
	}
	copyValue := append([]byte(nil), value...)
	defer clear(copyValue)
	credential := nativeCredential{
		Type:               credentialTypeGeneric,
		TargetName:         targetUTF16,
		CredentialBlobSize: uint32(len(copyValue)),
		CredentialBlob:     &copyValue[0],
		Persist:            credentialPersistLocalMachine,
	}
	result, _, callErr := procCredWrite.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if result == 0 {
		return callErr
	}
	return nil
}

func (*WindowsStore) Get(_ context.Context, provider string) ([]byte, error) {
	target, err := targetName(provider)
	if err != nil {
		return nil, aiprovider.ErrCredentialInvalid
	}
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return nil, aiprovider.ErrCredentialInvalid
	}
	var credential *nativeCredential
	result, _, callErr := procCredRead.Call(uintptr(unsafe.Pointer(targetUTF16)), credentialTypeGeneric, 0, uintptr(unsafe.Pointer(&credential)))
	if result == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return nil, aiprovider.ErrCredentialNotFound
		}
		return nil, callErr
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential == nil || credential.CredentialBlobSize == 0 || credential.CredentialBlob == nil {
		return nil, aiprovider.ErrCredentialNotFound
	}
	value := append([]byte(nil), unsafe.Slice(credential.CredentialBlob, int(credential.CredentialBlobSize))...)
	return value, nil
}

func (*WindowsStore) Delete(_ context.Context, provider string) error {
	target, err := targetName(provider)
	if err != nil {
		return aiprovider.ErrCredentialInvalid
	}
	targetUTF16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return aiprovider.ErrCredentialInvalid
	}
	result, _, callErr := procCredDelete.Call(uintptr(unsafe.Pointer(targetUTF16)), credentialTypeGeneric, 0)
	if result == 0 {
		if errors.Is(callErr, windows.ERROR_NOT_FOUND) {
			return aiprovider.ErrCredentialNotFound
		}
		return callErr
	}
	return nil
}
