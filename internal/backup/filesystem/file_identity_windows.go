//go:build windows

package filesystem

import "golang.org/x/sys/windows"

func validatePlatformFile(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ErrPathSecurity
	}
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var identity windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &identity); err != nil {
		return err
	}
	if identity.NumberOfLinks != 1 || identity.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return ErrPathSecurity
	}
	return nil
}
