package uploader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

// feed runs t through windows; speed returns the throughput the network
// gives at n parallel uploads. It returns the worker counts chosen.
func feed(t *tuner, windows int, speed func(n int) float64) []int {
	var got []int
	for range windows {
		got = append(got, t.next(speed(t.n), false))
	}
	return got
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTunerClimbsWhileFaster(t *testing.T) {
	// Throughput grows with each worker up to 10, then stays flat.
	speed := func(n int) float64 { return float64(min(n, 10)) * 1e6 }
	got := feed(newTuner(4, 16), 6, speed)
	// 4 measured → probe 6 → faster, probe 8 → faster, probe 10 → faster,
	// probe 12 → no gain, back to 10 → hold.
	want := []int{6, 8, 10, 12, 10, 10}
	if !equalInts(got, want) {
		t.Fatalf("workers = %v, want %v", got, want)
	}
}

func TestTunerRevertsWhenNotFaster(t *testing.T) {
	speed := func(int) float64 { return 5e6 } // flat: more workers don't help
	got := feed(newTuner(4, 16), 2, speed)
	if want := []int{6, 4}; !equalInts(got, want) {
		t.Fatalf("workers = %v, want %v", got, want)
	}
}

func TestTunerProbesAgainAfterHolding(t *testing.T) {
	speed := func(int) float64 { return 5e6 }
	tn := newTuner(4, 16)
	got := feed(tn, 2+tuneHoldWindows+1, speed)
	// probe, revert, hold for tuneHoldWindows windows, then probe again.
	if got[len(got)-1] != 6 {
		t.Fatalf("workers = %v, want a new probe to 6 after holding", got)
	}
	for _, n := range got[2 : 2+tuneHoldWindows] {
		if n != 4 {
			t.Fatalf("workers = %v, want 4 while holding", got)
		}
	}
}

func TestTunerHalvesOnCongestion(t *testing.T) {
	tn := newTuner(10, 16)
	if n := tn.next(9e6, true); n != 5 {
		t.Fatalf("after congestion n = %d, want 5", n)
	}
	// It holds before probing again.
	for i := range tuneHoldWindows {
		if n := tn.next(9e6, false); n != 5 {
			t.Fatalf("hold window %d: n = %d, want 5", i, n)
		}
	}
	if n := tn.next(9e6, true); n != 2 {
		t.Fatalf("after second congestion n = %d, want 2", n)
	}
	tn.next(1, true)
	if n := tn.next(1, true); n != 1 {
		t.Fatalf("n = %d, want never below 1", n)
	}
}

func TestTunerCongestionDuringProbeKeepsLessThanBefore(t *testing.T) {
	tn := newTuner(8, 16)
	tn.next(8e6, false) // probe 10
	if n := tn.next(8e6, true); n != 5 {
		t.Fatalf("n = %d, want half of the probed 10", n)
	}
	if k := tn.kept(); k != 5 {
		t.Fatalf("kept() = %d, want 5", k)
	}
}

func TestTunerStaysWithinMax(t *testing.T) {
	speed := func(n int) float64 { return float64(n) * 1e6 } // always faster
	got := feed(newTuner(12, 16), 4, speed)
	for _, n := range got {
		if n > 16 {
			t.Fatalf("workers = %v, want at most 16", got)
		}
	}
	if got[len(got)-1] != 16 {
		t.Fatalf("workers = %v, want to reach 16", got)
	}
}

func TestTunerKeptIgnoresUnjudgedProbe(t *testing.T) {
	tn := newTuner(4, 16)
	tn.next(4e6, false) // probing 6, not judged yet
	if k := tn.kept(); k != 4 {
		t.Fatalf("kept() = %d, want 4", k)
	}
}

func TestLimiterBoundsAndResizes(t *testing.T) {
	l := newLimiter(context.Background(), 2)
	var active, peak atomic.Int32
	var wg sync.WaitGroup
	release := make(chan struct{})
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !l.acquire() {
				return
			}
			n := active.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			<-release
			active.Add(-1)
			l.release()
		}()
	}
	waitFor(t, func() bool { return active.Load() == 2 })
	time.Sleep(20 * time.Millisecond)
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak = %d with limit 2", p)
	}
	l.setLimit(4)
	waitFor(t, func() bool { return active.Load() == 4 })
	close(release)
	wg.Wait()
	if p := peak.Load(); p != 4 {
		t.Fatalf("peak = %d after raising the limit to 4", p)
	}
}

func TestLimiterAcquireStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l := newLimiter(ctx, 1)
	if !l.acquire() {
		t.Fatal("first acquire failed")
	}
	done := make(chan bool)
	go func() { done <- l.acquire() }()
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("acquire succeeded after cancel, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquire still blocked after cancel")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached in time")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestUploadAllAutoUploadsEverythingAndRemembers runs a sync in automatic
// mode with very short windows: every file must still be uploaded once, and
// the worker count it settled on becomes the next sync's start.
func TestUploadAllAutoUploadsEverythingAndRemembers(t *testing.T) {
	withoutRetryDelays(t)
	saved := tuneWindow
	tuneWindow = time.Millisecond
	t.Cleanup(func() { tuneWindow = saved })

	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		requests.Add(1)
		time.Sleep(2 * time.Millisecond)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"a","status":"created"}`))
	}))
	defer srv.Close()

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	var files []scanner.File
	for i := range 60 {
		name := fmt.Sprintf("DSC%05d.JPG", i)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, scanner.File{AbsolutePath: path, RelativePath: "DCIM/" + name, Size: int64(len(name)), Mtime: time.Unix(int64(1000+i), 0)})
	}

	svc := &Service{State: st}
	if got := svc.startWorkers(); got != config.DefaultConcurrency {
		t.Fatalf("startWorkers() before any sync = %d, want %d", got, config.DefaultConcurrency)
	}
	session := newSyncSession(st, "vol", files)
	res, err := svc.uploadAll(context.Background(), "vol", immich.NewClient(srv.URL, "key"), svc.startWorkers(), true, len(files), 0, files,
		newSyncProgress(len(files), 0, nil), session, newSyncCheckpoint(st, session, 150))
	if err != nil {
		t.Fatalf("uploadAll() error = %v", err)
	}
	if res.Uploaded != len(files) || requests.Load() != int64(len(files)) {
		t.Fatalf("uploaded %d with %d requests, want %d each", res.Uploaded, requests.Load(), len(files))
	}
	if got := svc.startWorkers(); got < 1 || got > config.MaxConcurrency {
		t.Fatalf("startWorkers() after the sync = %d, want 1..%d", got, config.MaxConcurrency)
	}
}
