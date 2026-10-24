//go:build darwin

package volume

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"howett.net/plist"
)

const volumesRoot = "/Volumes"

func ListMounted() ([]Info, error) {
	entries, err := os.ReadDir(volumesRoot)
	if err != nil {
		return nil, err
	}

	var result []Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mountPoint := filepath.Join(volumesRoot, e.Name())
		info, err := GetInfo(mountPoint)
		if err != nil {
			continue
		}
		result = append(result, *info)
	}
	return result, nil
}

func ListRemovable() ([]Info, error) {
	all, err := ListMounted()
	if err != nil {
		return nil, err
	}
	var result []Info
	for _, v := range all {
		if v.Removable {
			result = append(result, v)
		}
	}
	return result, nil
}

func GetInfo(mountPoint string) (*Info, error) {
	out, err := exec.Command("diskutil", "info", "-plist", mountPoint).Output()
	if err != nil {
		return nil, fmt.Errorf("diskutil info %s: %w", mountPoint, err)
	}

	var data map[string]any
	if _, err := plist.Unmarshal(out, &data); err != nil {
		return nil, err
	}

	info := &Info{MountPoint: mountPoint}

	if v, ok := data["VolumeName"].(string); ok {
		info.Name = v
	} else {
		info.Name = filepath.Base(mountPoint)
	}
	if v, ok := data["VolumeUUID"].(string); ok {
		info.UUID = strings.ToUpper(v)
	}
	if v, ok := data["Removable"].(bool); ok && v {
		info.Removable = true
	}
	if v, ok := data["Ejectable"].(bool); ok && v {
		info.Removable = true
	}
	if v, ok := data["External"].(bool); ok && v {
		info.Removable = true
	}

	// Skip the system volume symlink
	if info.Name == "Macintosh HD" || mountPoint == "/" {
		info.Removable = false
	}

	return info, nil
}

func FindByUUID(uuid string) (*Info, error) {
	uuid = strings.ToUpper(uuid)
	volumes, err := ListMounted()
	if err != nil {
		return nil, err
	}
	for _, v := range volumes {
		if strings.ToUpper(v.UUID) == uuid {
			return &v, nil
		}
	}
	return nil, nil
}

func WaitForMount(mountPoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(mountPoint); err == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("mount point %s not ready", mountPoint)
}
