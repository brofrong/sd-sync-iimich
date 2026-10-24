//go:build !windows

package gui

import "syscall"

// volumeSize returns the total size of the file system mounted at
// mountPoint, or 0 if it cannot be determined.
func volumeSize(mountPoint string) int64 {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(mountPoint, &fs); err != nil {
		return 0
	}
	return int64(fs.Blocks) * int64(fs.Bsize)
}
