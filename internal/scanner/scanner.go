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
	// Lower-cased absolute paths of XMP files, the only sidecar format Immich accepts.
	xmps := make(map[string]string)

	for _, relDir := range cfg.ScanPaths() {
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
			if ShouldExclude(name, cfg.Scan.ExcludePatterns) {
				return nil
			}
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
			if ext == "xmp" {
				xmps[strings.ToLower(path)] = path
				return nil
			}
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

			files = append(files, File{
				AbsolutePath: path,
				RelativePath: rel,
				Size:         info.Size(),
				Mtime:        info.ModTime(),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", root, err)
		}
	}
	for i := range files {
		files[i].SidecarPath = findSidecar(files[i].AbsolutePath, xmps)
	}
	return files, nil
}

// ShouldExclude reports whether name matches one of the configured exclude
// patterns ("._*" is a special case for macOS AppleDouble sidecar junk; the
// rest are plain filepath.Match globs against the base name), so callers
// outside this package (card verification) can skip the same camera junk
// the scanner does without re-walking its logic.
func ShouldExclude(name string, patterns []string) bool {
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

// findSidecar returns the XMP sidecar for a media file ("IMG.JPG.xmp" or "IMG.xmp").
// Camera XML metadata such as Sony "C0001M01.XML" is not XMP: Immich rejects the
// whole upload with "Unsupported file type" when it is sent as a sidecar.
func findSidecar(mediaPath string, xmps map[string]string) string {
	withoutExt := strings.TrimSuffix(mediaPath, filepath.Ext(mediaPath))
	for _, c := range []string{mediaPath + ".xmp", withoutExt + ".xmp"} {
		if p, ok := xmps[strings.ToLower(c)]; ok {
			return p
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
