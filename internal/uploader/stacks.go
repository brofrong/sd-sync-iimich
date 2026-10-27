package uploader

import (
	"context"
	"errors"
	"net/http"
	"path"
	"slices"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
)

// coverExtensions are the formats a camera saves next to a RAW, in the
// order they are preferred as the stack's cover.
var coverExtensions = []string{"jpg", "jpeg", "jpe", "heic", "heif", "hif"}

// stackRank orders a group's files: covers first (by coverExtensions), then
// RAWs. ok is false for a file that is neither.
func stackRank(f scanner.File) (rank int, ok bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(f.RelativePath), "."))
	if i := slices.Index(coverExtensions, ext); i >= 0 {
		return i, true
	}
	if config.IsRawExtension(ext) {
		return len(coverExtensions), true
	}
	return 0, false
}

// rawJPEGGroups finds the shots saved as RAW+JPEG (or RAW+HEIF): files in
// the same folder with the same name, at least one RAW and one JPEG or
// HEIF. Each group lists its cover first (a JPEG before a HEIF), then the
// rest by path; the groups come in path order.
func rawJPEGGroups(files []scanner.File) [][]scanner.File {
	byShot := make(map[string][]scanner.File)
	for _, f := range files {
		if _, ok := stackRank(f); !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSuffix(f.RelativePath, path.Ext(f.RelativePath)))
		byShot[key] = append(byShot[key], f)
	}

	var groups [][]scanner.File
	for _, g := range byShot {
		slices.SortFunc(g, func(a, b scanner.File) int {
			ra, _ := stackRank(a)
			rb, _ := stackRank(b)
			if ra != rb {
				return ra - rb
			}
			return strings.Compare(a.RelativePath, b.RelativePath)
		})
		first, _ := stackRank(g[0])
		last, _ := stackRank(g[len(g)-1])
		if first < len(coverExtensions) && last == len(coverExtensions) {
			groups = append(groups, g)
		}
	}
	slices.SortFunc(groups, func(a, b []scanner.File) int {
		return strings.Compare(a[0].RelativePath, b[0].RelativePath)
	})
	return groups
}

// stackPairs stacks in Immich each group with a file synced in session, the
// cover on top, and returns how many it stacked. A member whose asset id
// the upload did not return (synced earlier, left out of a retry, or a
// duplicate Immich answered before reading it) is found by its checksum; a
// group with a member that failed in this session, or that Immich does not
// have, is left for a later sync. Stacking never fails the sync: a key without
// stack.create, or a server that stops answering, just ends it.
func (s *Service) stackPairs(ctx context.Context, client *immich.Client, volumeUUID string, groups [][]scanner.File, session *syncSession) int {
	if len(groups) == 0 {
		return 0
	}
	session.mu.Lock()
	ids := make(map[string]string, len(session.assets))
	for p, id := range session.assets {
		ids[p] = id
	}
	synced := make(map[string]bool, len(session.completed))
	for p := range session.completed {
		synced[p] = true
	}
	session.mu.Unlock()
	inSession := make(map[string]bool, len(session.order))
	for _, f := range session.order {
		inSession[f.RelativePath] = true
	}

	var todo [][]scanner.File
	var lookup []immich.BulkCheckAsset
	for _, g := range groups {
		fresh, failed := false, false
		for _, f := range g {
			switch {
			case synced[f.RelativePath]:
				fresh = true
			case inSession[f.RelativePath]:
				failed = true
			}
		}
		if !fresh || failed {
			continue
		}
		todo = append(todo, g)
		for _, f := range g {
			if ids[f.RelativePath] != "" {
				continue
			}
			sha1hex, err := s.fileSHA1(volumeUUID, f)
			if err != nil {
				logx.Warn("sync: stack: sha1 failed", "file", f.RelativePath, "err", err)
				continue
			}
			lookup = append(lookup, immich.BulkCheckAsset{ID: f.RelativePath, Checksum: sha1hex})
		}
	}

	for start := 0; start < len(lookup); start += verifyBatchSize {
		batch := lookup[start:min(start+verifyBatchSize, len(lookup))]
		results, err := client.BulkUploadCheck(ctx, batch)
		if err != nil {
			logx.Warn("sync: stack: looking up the other half of pairs failed", "err", err)
			break
		}
		for _, r := range results {
			if r.Action == "reject" && r.Reason == "duplicate" && !r.IsTrashed && r.AssetID != "" {
				ids[r.ID] = r.AssetID
			}
		}
	}

	stacked := 0
	for _, g := range todo {
		assetIDs := make([]string, 0, len(g))
		for _, f := range g {
			if id := ids[f.RelativePath]; id != "" {
				assetIDs = append(assetIDs, id)
			}
		}
		if len(assetIDs) < len(g) {
			continue
		}
		if err := client.CreateStack(ctx, assetIDs); err != nil {
			var se *immich.StatusError
			if errors.As(err, &se) && se.Code == http.StatusForbidden {
				logx.Warn("sync: stack: the API key lacks the stack.create permission, pairs are not stacked", "err", err)
				break
			}
			logx.Warn("sync: stack failed", "file", g[0].RelativePath, "err", err)
			if ctx.Err() != nil || immich.IsRetryable(err) {
				break
			}
			continue
		}
		stacked++
	}
	if stacked > 0 {
		logx.Info("sync: stacked RAW+JPEG pairs", "stacks", stacked)
	}
	return stacked
}
