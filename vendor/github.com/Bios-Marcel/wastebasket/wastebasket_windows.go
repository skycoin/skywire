package wastebasket

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32DLL         = windows.NewLazyDLL("shell32.dll")
	shFileOperationW   = shell32DLL.NewProc("SHFileOperationW")
	shEmptyRecycleBinW = shell32DLL.NewProc("SHEmptyRecycleBinW")
)

/*
#define FO_DELETE         0x0003

#define FOF_SILENT                 0x0004
#define FOF_NOCONFIRMATION         0x0010
#define FOF_ALLOWUNDO              0x0040
#define FOF_NOCONFIRMMKDIR         0x0200
#define FOF_NOERRORUI              0x0400
#define FOF_NO_UI                  (FOF_SILENT | FOF_NOCONFIRMATION | FOF_NOERRORUI | FOF_NOCONFIRMMKDIR)

typedef struct _SHFILEOPSTRUCTW {
  HWND         hwnd;
  UINT         wFunc;
  PCZZWSTR     pFrom;
  PCZZWSTR     pTo;
  FILEOP_FLAGS fFlags;
  BOOL         fAnyOperationsAborted;
  LPVOID       hNameMappings;
  PCWSTR       lpszProgressTitle;
} SHFILEOPSTRUCTW, *LPSHFILEOPSTRUCTW;
*/

const (
	FO_DELETE = 0x0003

	FOF_SILENT         = 0x0004
	FOF_NOCONFIRMATION = 0x0010
	FOF_ALLOWUNDO      = 0x0040
	FOF_NOCONFIRMMKDIR = 0x0200
	FOF_NOERRORUI      = 0x0400
	FOF_NO_UI          = FOF_SILENT | FOF_NOCONFIRMATION | FOF_NOERRORUI | FOF_NOCONFIRMMKDIR
)

type SHFileOpStructW struct {
	// Irrelevant, as its for UI related things
	hwnd windows.HWND

	wFunc uint32
	pFrom *uint16
	// This isn't necessary as it is only needed for copy / move actions.
	pTo    *uint16
	fFlags uint16

	// FIXME Why isn't this relevant?
	fAnyOperationsAborted int32
	// Irrelevant, as its for move operations
	hNameMappings uintptr
	// Irrelevant, as its for UI related things
	lpszProgressTitle *uint16
}

// Trash moves a file or folder including its content into the systems trashbin.
func Trash(paths ...string) error {
	existingPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		// The API will return error code "2 - Operation completed successfully"
		// when attempting to delete a non-existent file.
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}

		existingPaths = append(existingPaths, path)
	}

	filesParameter, err := makeDoubleNullTerminatedLpstr(existingPaths...)
	if err != nil {
		return fmt.Errorf("error creating utf16ptr for passed path: %w", err)
	}

	ret, _, err := shFileOperationW.Call(uintptr(unsafe.Pointer(&SHFileOpStructW{
		hwnd:                  windows.HWND(0),
		wFunc:                 FO_DELETE,
		pFrom:                 filesParameter,
		pTo:                   nil,
		fFlags:                FOF_NO_UI | FOF_ALLOWUNDO,
		fAnyOperationsAborted: 0,
		hNameMappings:         0,
		lpszProgressTitle:     nil,
	})))

	if ret != 0 {
		return fmt.Errorf("windows error: %w", err)
	}

	return nil
}

func makeDoubleNullTerminatedLpstr(items ...string) (*uint16, error) {
	chars := []uint16{}
	for _, s := range items {
		converted, err := windows.UTF16FromString(s)
		if err != nil {
			return nil, err
		}
		chars = append(chars, converted...)
	}
	chars = append(chars, 0)
	return &chars[0], nil
}

const (
	SHERB_NOCONFIRMATION = 1
	SHERB_NOPROGRESSUI   = 2
	SHERB_NOSOUND        = 4
)

// Empty clears the platforms trashbin.
func Empty() error {
	flags := SHERB_NOCONFIRMATION | SHERB_NOPROGRESSUI | SHERB_NOSOUND

	ret, _, err := shEmptyRecycleBinW.Call(uintptr(unsafe.Pointer(nil)), uintptr(unsafe.Pointer(nil)), uintptr(flags))
	if ret != 0 {
		// Weird edge case, where windows reports that it couldnt load the DLL
		// if the trash bin is empty.
		if err.(windows.Errno) == 126 {
			return nil
		}

		return fmt.Errorf("windows error: %w", err)
	}

	return nil
}
