package scanner

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
)

type File struct {
	AbsolutePath string
	RelativePath string
	Size         int64
	Mtime        time.Time
	SHA1         string
	SidecarPath  string
}

func ScanVolume(mountPoint string, cfg config.Config) ([]File, error) {
	exts := cfg.ExtensionSet()
	var files []File

	for _, relDir := range cfg.Scan.Paths {
		root := filepath.Join(mountPoint, relDir)
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			name := d.Name()
			if shouldExclude(name, cfg.Scan.ExcludePatterns) {
				return nil
			}
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
			if _, ok := exts[ext]; !ok {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(mountPoint, path)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)

			f := File{
				AbsolutePath: path,
				RelativePath: rel,
				Size:         info.Size(),
				Mtime:        info.ModTime(),
			}
			if ext == "mp4" {
				f.SidecarPath = findSidecar(path)
			}
			files = append(files, f)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", root, err)
		}
	}
	return files, nil
}

func shouldExclude(name string, patterns []string) bool {
	for _, p := range patterns {
		if p == "._*" && strings.HasPrefix(name, "._") {
			return true
		}
		matched, _ := filepath.Match(p, name)
		if matched {
			return true
		}
	}
	return false
}

func findSidecar(videoPath string) string {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	candidates := []string{
		filepath.Join(dir, base+"M01.XML"),
		filepath.Join(dir, base+".XML"),
		filepath.Join(dir, base+".xml"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func ComputeSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
