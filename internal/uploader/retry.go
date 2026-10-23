package uploader

import (
	"context"
	"errors"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// retryDelays are the pauses before each repeated attempt of a transient failure.
var retryDelays = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}

// ErrServerUnavailable stops a sync once a file still fails with a network or
// server error after all retries, instead of burning through the rest of the card.
var ErrServerUnavailable = errors.New("immich server unavailable")

// ErrAuthFailed stops a sync immediately when the server rejects the API key
// (401/403): retrying would not help, and continuing would burn through the
// rest of the card reporting the same failure for every file.
var ErrAuthFailed = errors.New("immich rejected the api key")

func uploadWithRetry(ctx context.Context, upload func() (*immich.UploadResponse, error)) (*immich.UploadResponse, error) {
	for attempt := 0; ; attempt++ {
		resp, err := upload()
		if err == nil || !immich.IsRetryable(err) || attempt >= len(retryDelays) {
			return resp, err
		}

		delay := retryDelays[attempt]
		logx.Warn("sync: upload failed, retrying", "attempt", attempt+1, "in", delay, "err", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
