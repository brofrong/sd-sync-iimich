package uploader

import (
	"context"
	"fmt"
	"io/fs"
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

// ignoredRootSystemDirs are macOS (or, for "System Volume Information",
// Windows) filesystem bookkeeping directories that an OS creates at the
// root of any card it mounts. They hold no user media and must never
// influence a format decision — independent of the user's
// Scan.ExcludePatterns, which the gate otherwise does not consult (see the
// Unsupported field). They are matched only as the first path segment (the
// card root): a folder of the same name deeper down, e.g.
// "DCIM/100MSDCF/.Trashes", is not OS bookkeeping and is walked normally.
var ignoredRootSystemDirs = []string{
	".Spotlight-V100",
	".fseventsd",
	".Trashes",
	".TemporaryItems",
	"System Volume Information",
}

// ignoredRootSystemFiles are Finder volume files that only mean something
// at the card root (a custom volume icon, the AirPort Disk marker).
var ignoredRootSystemFiles = []string{
	".VolumeIcon.icns",
	".apdisk",
}

// ".DS_Store" files (Finder writes one into every folder it opens) and
// AppleDouble sidecar files ("._foo") are ignored at any depth; see
// isIgnoredSystemFile.

// ignoredMediaDirs are known camera thumbnail/proxy directories: separate,
// disposable renditions Immich never needs and the uploader never scans.
// The card-lifecycle spec lists thumbnails and proxies as not blocking a
// format, so their presence (even unconfirmed) must not count against
// VerifyResult.Safe.
var ignoredMediaDirs = []string{
	"PRIVATE/M4ROOT/THMBNL",
	"PRIVATE/M4ROOT/SUB",
	"XDROOT/Sub",
	"XDROOT/Thmbnl",
	"PRIVATE/XDROOT/Sub",
	"PRIVATE/XDROOT/Thmbnl",
}

// ignoredServiceExtensions are known camera/sidecar service-file formats
// the spec lists as not blocking a format: xml (clip metadata, e.g. Sony
// "C0001M01.XML"), bnp/inp/int/bin/dat/cpi/mpl/bdm (AVCHD/XDCAM index and
// database files), thm (separate still thumbnails), lrv/lrf (GoPro low-res
// proxies and their preview index), ctg (Canon catalog), idx/smi (subtitle
// or index sidecars some cameras write), ind (Sony card database,
// "PRIVATE/SONY/SONYCARD.IND"), tdt/tid (AVCHD thumbnail database,
// "AVCHDTN/THUMB.TDT"/".TID"), dsc (Nikon card database), mrk (Canon
// "MISC/*.MRK" print/transfer orders), and xmp (Immich sidecars — the
// scanner uploads an XMP together with its media file, never separately,
// so it is never itself "missing" from Immich).
var ignoredServiceExtensions = map[string]struct{}{
	"xml": {}, "bnp": {}, "inp": {}, "int": {}, "bin": {}, "dat": {},
	"cpi": {}, "mpl": {}, "bdm": {}, "thm": {}, "lrv": {}, "lrf": {},
	"ctg": {}, "idx": {}, "smi": {}, "xmp": {}, "ind": {}, "tdt": {},
	"tid": {}, "dsc": {}, "mrk": {},
}

// VerifyResult is the outcome of checking a whole card's media files
// against Immich before the card may be formatted (erased). It is the
// safety gate: Safe must be true before anything may erase the card.
type VerifyResult struct {
	// Media is the number of media files inside the configured scan paths
	// and extension set that were considered for the check — including
	// ones whose metadata could not even be read (they still count,
	// strictly, as present-but-unconfirmed media).
	Media int
	// MissingCount is how many of those files Immich did not confirm
	// already has (not reported back, not a reject/duplicate match, or
	// reported but trashed) or that could not even be hashed.
	MissingCount int
	// Missing lists the relative paths of the first files found missing,
	// up to maxMissingListed.
	Missing []string
	// Unsupported counts files that would be erased by a format without
	// ever being checked: a recognized camera media format outside the
	// configured scan paths or extension set (or excluded by the user's
	// own Scan.ExcludePatterns, since the scanner would never upload it),
	// or any other file this gate does not otherwise recognize as pure
	// camera bookkeeping (documents, unknown extensions, no extension at
	// all — keyed "(none)"). Keyed by lower-case extension without a dot.
	Unsupported map[string]int
}

// Safe reports whether the card may be formatted: it holds at least one
// media file, every media file inside the configured scan paths and
// extensions was confirmed already stored (and not trashed) on the Immich
// server, and no file was found outside that recognized, confirmable
// scope. An empty (or never-synced) card is never reported safe: Media
// must be greater than zero.
func (r VerifyResult) Safe() bool {
	return r.Media > 0 && r.MissingCount == 0 && len(r.Unsupported) == 0
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
// consult via VerifyResult.Safe before erasing the card.
//
// It fails closed: an unreadable mount point or subdirectory, a file that
// can't be related to the mount point, a bulk-upload-check request that
// fails, or a cancelled context all make VerifyCard return a non-nil error
// (never a "safe-looking" empty/partial result) rather than silently
// skipping what it couldn't look at.
//
// onProgress, when non-nil, is called once before the first batch
// (done == 0) and again after each batch of up to 200 files finishes its
// round trip to Immich, with done counting files checked so far and total
// the number of media files being checked overall.
func (s *Service) VerifyCard(ctx context.Context, vol volume.Info, onProgress func(done, total int)) (VerifyResult, error) {
	if err := ctx.Err(); err != nil {
		return VerifyResult{}, fmt.Errorf("verify card: %w", err)
	}

	mountInfo, err := os.Stat(vol.MountPoint)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("verify card: %w", err)
	}
	if !mountInfo.IsDir() {
		return VerifyResult{}, fmt.Errorf("verify card: %s is not a directory", vol.MountPoint)
	}

	cfg := s.Config.Get()
	extSet := cfg.ExtensionSet()
	knownMedia := config.KnownMediaExtensions()

	var result VerifyResult
	var candidates []verifyCandidate

	walkErr := filepath.WalkDir(vol.MountPoint, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Fail closed: an unreadable directory (permissions, an
			// ejected card) must stop verification rather than silently
			// treat the files inside it as if they didn't exist.
			return fmt.Errorf("verify card: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		rel, relErr := filepath.Rel(vol.MountPoint, path)
		if relErr != nil {
			return fmt.Errorf("verify card: %w", relErr)
		}
		rel = filepath.ToSlash(rel)
		name := d.Name()

		if d.IsDir() {
			if isIgnoredRootSystemDir(rel) {
				return fs.SkipDir
			}
			return nil
		}

		if isIgnoredSystemFile(name, rel) || underIgnoredMediaDir(rel) {
			return nil
		}

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		if _, ignored := ignoredServiceExtensions[ext]; ignored {
			return nil
		}

		_, knownMediaExt := knownMedia[ext]
		_, configuredExt := extSet[ext]
		if !knownMediaExt && !configuredExt {
			addUnsupported(&result, extKey(ext))
			return nil
		}

		if !configuredExt || !cfg.InScanPaths(rel) || scanner.ShouldExclude(name, cfg.Scan.ExcludePatterns) {
			// Outside the configured scan scope, outside the configured
			// extension set, or excluded by the user's own patterns: the
			// scanner would never upload this file, so a format must not
			// be allowed to treat it as already safe.
			addUnsupported(&result, extKey(ext))
			return nil
		}

		result.Media++
		info, infoErr := d.Info()
		if infoErr != nil {
			// Unreadable now (e.g. removed mid-walk): strict means this
			// still counts as media, but not confirmed.
			addMissing(&result, rel)
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
	if walkErr != nil {
		return VerifyResult{}, walkErr
	}

	total := len(candidates)
	if onProgress != nil {
		onProgress(0, total)
	}
	if total == 0 {
		return result, nil
	}

	client := immich.NewClient(cfg.Immich.URL, cfg.Immich.APIKey)
	done := 0

	for start := 0; start < len(candidates); start += verifyBatchSize {
		if err := ctx.Err(); err != nil {
			return VerifyResult{}, fmt.Errorf("verify card: %w", err)
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
				addMissing(&result, c.relativePath)
				continue
			}
			assets = append(assets, immich.BulkCheckAsset{ID: c.relativePath, Checksum: sha1hex})
		}

		if len(assets) > 0 {
			results, checkErr := client.BulkUploadCheck(ctx, assets)
			if checkErr != nil {
				return VerifyResult{}, fmt.Errorf("verify card: %w", checkErr)
			}

			// Immich's response is asked about by id; if it answers more
			// than once for the same id, the worst answer wins — a single
			// confirming entry must never paper over another that isn't.
			confirmedByID := make(map[string]bool, len(results))
			answered := make(map[string]bool, len(results))
			for _, r := range results {
				confirmed := r.Action == "reject" && r.Reason == "duplicate" && !r.IsTrashed
				if answered[r.ID] {
					confirmedByID[r.ID] = confirmedByID[r.ID] && confirmed
				} else {
					confirmedByID[r.ID] = confirmed
					answered[r.ID] = true
				}
			}
			for _, a := range assets {
				// A map lookup for an id Immich's response omitted
				// entirely defaults to false: not confirmed.
				if !confirmedByID[a.ID] {
					addMissing(&result, a.ID)
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

// extKey returns the VerifyResult.Unsupported map key for ext (a lower-case
// extension without a dot, possibly empty for a file with none).
func extKey(ext string) string {
	if ext == "" {
		return "(none)"
	}
	return ext
}

func addUnsupported(result *VerifyResult, key string) {
	if result.Unsupported == nil {
		result.Unsupported = make(map[string]int)
	}
	result.Unsupported[key]++
}

func addMissing(result *VerifyResult, rel string) {
	result.MissingCount++
	if len(result.Missing) < maxMissingListed {
		result.Missing = append(result.Missing, rel)
	}
}

// isIgnoredRootSystemDir reports whether rel (a directory relative to the
// card root) is one of ignoredRootSystemDirs sitting directly at the root.
// The walk skips such a directory entirely: never counted as media,
// missing, or unsupported, and never sent to Immich. Compared
// case-insensitively: macOS SD cards are case-insensitive FAT/exFAT.
func isIgnoredRootSystemDir(rel string) bool {
	if strings.Contains(rel, "/") {
		return false
	}
	return equalFoldAny(rel, ignoredRootSystemDirs)
}

// isIgnoredSystemFile reports whether the file name at rel is macOS
// bookkeeping the format gate must disregard: an AppleDouble sidecar
// ("._DSC00001.JPG") or a ".DS_Store" at any depth, or one of
// ignoredRootSystemFiles directly at the card root.
func isIgnoredSystemFile(name, rel string) bool {
	if strings.HasPrefix(name, "._") || strings.EqualFold(name, ".DS_Store") {
		return true
	}
	return !strings.Contains(rel, "/") && equalFoldAny(name, ignoredRootSystemFiles)
}

func equalFoldAny(s string, list []string) bool {
	for _, item := range list {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

// underIgnoredMediaDir reports whether rel sits at or under one of
// ignoredMediaDirs, compared case-insensitively: macOS SD cards are
// case-insensitive FAT/exFAT.
func underIgnoredMediaDir(rel string) bool {
	relLower := strings.ToLower(rel)
	for _, dir := range ignoredMediaDirs {
		dirLower := strings.ToLower(dir)
		if relLower == dirLower || strings.HasPrefix(relLower, dirLower+"/") {
			return true
		}
	}
	return false
}
