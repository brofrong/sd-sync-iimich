package gui

import (
	"sync"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/i18n"
)

func loadCatalog(t *testing.T, lang string) *i18n.Catalog {
	t.Helper()
	c, err := i18n.Load(lang)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTrayStatusLine(t *testing.T) {
	en := loadCatalog(t, "en")
	progress := func(current, total, eta int) app.Status {
		return app.Status{Code: app.CodeSyncing, Params: map[string]string{}, Progress: &app.Progress{Current: current, Total: total, ETASeconds: eta}}
	}
	tests := []struct {
		name string
		st   app.Status
		want string
	}{
		{"idle code shows its title", app.Status{Code: app.CodeWaitingForCard, Params: map[string]string{"name": "SD"}}, "Waiting for card"},
		{"syncing without progress yet", app.Status{Code: app.CodeSyncing}, "Syncing"},
		{"syncing with an empty plan", progress(0, 0, 0), "Syncing"},
		{"no ETA yet", progress(5, 100, 0), "Syncing 5 of 100"},
		{"seconds round up to a minute", progress(5, 100, 20), "Syncing 5 of 100 · about 1 min left"},
		{"minutes", progress(5, 100, 170), "Syncing 5 of 100 · about 3 min left"},
		{"hours", progress(5, 100, 2*3600+14*60), "Syncing 5 of 100 · about 2 h 14 min left"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := trayStatusLine(en, tt.st); got != tt.want {
				t.Fatalf("trayStatusLine = %q, want %q", got, tt.want)
			}
		})
	}

	ru := loadCatalog(t, "ru")
	if got, want := trayStatusLine(ru, progress(5, 100, 170)), "Синхронизация 5 из 100 · осталось ~3 мин"; got != want {
		t.Fatalf("ru trayStatusLine = %q, want %q", got, want)
	}
}

// TestTrayLineAtMostOncePerSecond checks the tray's status line throttle:
// progress ticks reach the menu at most once per second, the latest one
// last; a code change goes out at once.
func TestTrayLineAtMostOncePerSecond(t *testing.T) {
	if trayLineInterval < time.Second {
		t.Fatalf("trayLineInterval = %v, want at least 1s", trayLineInterval)
	}
	var (
		mu  sync.Mutex
		got []app.Status
	)
	line := newTrayLine(func(st app.Status) {
		mu.Lock()
		got = append(got, st)
		mu.Unlock()
	})
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(got)
	}

	for i := 1; i <= 20; i++ {
		line.Push(syncing(i))
		time.Sleep(10 * time.Millisecond)
	}
	if n := count(); n != 1 {
		t.Fatalf("%d updates within %v, want 1", n, trayLineInterval)
	}
	time.Sleep(trayLineInterval + 300*time.Millisecond)
	mu.Lock()
	n, last := len(got), got[len(got)-1]
	mu.Unlock()
	if n != 2 || last.Progress.Current != 20 {
		t.Fatalf("after the interval: %d updates, last progress %d; want 2, 20", n, last.Progress.Current)
	}

	line.Push(app.Status{Code: app.CodeDone})
	if n := count(); n != 3 {
		t.Fatalf("a code change was held back (%d updates)", n)
	}
}
