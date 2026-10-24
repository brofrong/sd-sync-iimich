package gui

import "golang.org/x/sys/windows"

// volumeSize returns the total size of the file system mounted at
// mountPoint (a drive such as `E:\`), or 0 if it cannot be determined.
func volumeSize(mountPoint string) int64 {
	p, err := windows.UTF16PtrFromString(mountPoint)
	if err != nil {
		return 0
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0
	}
	return int64(total)
}
