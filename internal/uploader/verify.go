package uploader

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// verifyBatchSize is how many assets VerifyCard sends to Immich's
// bulk-upload-check per request.
const verifyBatchSize = 200

// maxMissingListed caps how many relative paths VerifyResult.Missing keeps,
// so a card with thousands of missing files doesn't blow up memory or the
// UI; MissingCount still reflects every one of them.
const maxMissingListed = 20

// VerifyResult is the outcome of checking a whole card's media files
// against Immich before the card may be formatted (erased). It is the
// safety gate: Safe must be true before anything may erase the card.
type VerifyResult struct {
	// Media is the number of media files that were checked against Immich
	// (inside a configured scan path, with a configured extension).
	Media int
	// MissingCount is how many of those files Immich did not confirm
	// already has (not reported back, not a reject/duplicate, or reported
	// but trashed).
	MissingCount int
	// Missing lists the relative paths of the first files found missing,
	// up to maxMissingListed.
	Missing []string
	// Unsupported counts media files (recognized camera formats) that sit
	// outside the configured scan paths, or whose extension is outside the
	// configured extension set, keyed by lower-case extension without a
	// dot. A format would erase these without ever checking them, so their
	// presence alone makes the card unsafe to format.
	Unsupported map[string]int
}

// Safe reports whether the card may be formatted: every media file inside
// the configured scan paths and extensions was confirmed already stored
// (and not trashed) on the Immich server, and no media file was found
// outside that configured scope.
func (r VerifyResult) Safe() bool {
	return r.MissingCount == 0 && len(r.Unsupported) == 0
}

// verifyCandidate is a media file inside the configured scan paths and
// extension set, found while walking the card, waiting to be checked
// against Immich.
type verifyCandidate struct {
	relativePath string
	absolutePath string
	size         int64
	mtime        time.Time
}

// VerifyCard walks the whole card at vol.MountPoint — not just the
// configured scan paths — and confirms every media file inside those scan
// paths (with a configured extension) is already stored on the Immich
// server and not trashed there. It is the safety gate a later format must
// consult via VerifyResult.Safe before erasing the card: a media file
// outside the configured scan paths/extensions, a file Immich doesn't
// confirm, or a context cancellation all make the result unsafe (or, for
// cancellation, return an error instead of a result).
//
// onProgress, when non-nil, is called as each batch of up to 200 files
// finishes its round trip to Immich, with done counting files checked so
// far and total the number of media files being checked overall.
func (s *Service) VerifyCard(ctx context.Context, vol volume.Info, onProgress func(done, total int)) (VerifyResult, error) {
	cfg := s.Config.Get()
	scanPaths := cfg.ScanPaths()
	extSet := cfg.ExtensionSet()
	knownMedia := config.KnownMediaExtensions()

	var result VerifyResult
	var candidates []verifyCandidate

	err := filepath.WalkDir(vol.MountPoint, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Unreadable directory entry: skip it rather than fail the
			// whole walk, matching ScanVolume's tolerance of transient OS
			// errors (ejected card, permission quirk).
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		name := d.Name()
		if scanner.ShouldExclude(name, cfg.Scan.ExcludePatterns) {
			return nil
		}

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		_, configured := extSet[ext]
		_, known := knownMedia[ext]
		if !configured && !known {
			// Not a media file by any definition: camera metadata/junk
			// (XML, BNP, ...) that must never influence the result.
			return nil
		}

		rel, relErr := filepath.Rel(vol.MountPoint, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		if !configured || !insideScanPaths(rel, scanPaths) {
			if result.Unsupported == nil {
				result.Unsupported = make(map[string]int)
			}
			result.Unsupported[ext]++
			return nil
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			// Unreadable now (e.g. removed mid-walk): strict means this
			// counts against safety, not silently skipped.
			result.MissingCount++
			if len(result.Missing) < maxMissingListed {
				result.Missing = append(result.Missing, rel)
			}
			return nil
		}

		candidates = append(candidates, verifyCandidate{
			relativePath: rel,
			absolutePath: path,
			size:         info.Size(),
			mtime:        info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return VerifyResult{}, err
	}

	result.Media = len(candidates)
	total := len(candidates)
	done := 0
	if onProgress != nil {
		onProgress(done, total)
	}
	if total == 0 {
		return result, nil
	}

	client := immich.NewClient(cfg.Immich.URL, cfg.Immich.APIKey)

	for start := 0; start < len(candidates); start += verifyBatchSize {
		if err := ctx.Err(); err != nil {
			return VerifyResult{}, err
		}
		end := min(start+verifyBatchSize, len(candidates))
		batch := candidates[start:end]

		assets := make([]immich.BulkCheckAsset, 0, len(batch))
		for _, c := range batch {
			sha1hex, hashErr := s.fileSHA1(vol.UUID, scanner.File{
				AbsolutePath: c.absolutePath,
				RelativePath: c.relativePath,
				Size:         c.size,
				Mtime:        c.mtime,
			})
			if hashErr != nil {
				// Unreadable file: strict, so it counts as not confirmed
				// rather than being silently dropped from the check.
				result.MissingCount++
				if len(result.Missing) < maxMissingListed {
					result.Missing = append(result.Missing, c.relativePath)
				}
				continue
			}
			assets = append(assets, immich.BulkCheckAsset{ID: c.relativePath, Checksum: sha1hex})
		}

		if len(assets) > 0 {
			results, checkErr := client.BulkUploadCheck(ctx, assets)
			if checkErr != nil {
				return VerifyResult{}, checkErr
			}
			byID := make(map[string]immich.BulkCheckResult, len(results))
			for _, r := range results {
				byID[r.ID] = r
			}
			for _, a := range assets {
				r, ok := byID[a.ID]
				confirmed := ok && r.Action == "reject" && r.Reason == "duplicate" && !r.IsTrashed
				if !confirmed {
					result.MissingCount++
					if len(result.Missing) < maxMissingListed {
						result.Missing = append(result.Missing, a.ID)
					}
				}
			}
		}

		done += len(batch)
		if onProgress != nil {
			onProgress(done, total)
		}
	}

	return result, nil
}

// insideScanPaths reports whether rel (a file's slash-separated path
// relative to the card's mount point) equals one of scanPaths or is nested
// under one, compared case-insensitively: macOS SD cards are case-
// insensitive FAT/exFAT, so "dcim/a.jpg" is inside "DCIM".
func insideScanPaths(rel string, scanPaths []string) bool {
	relLower := strings.ToLower(rel)
	for _, sp := range scanPaths {
		spLower := strings.ToLower(strings.TrimSuffix(sp, "/"))
		if spLower == "" {
			continue
		}
		if relLower == spLower || strings.HasPrefix(relLower, spLower+"/") {
			return true
		}
	}
	return false
}
