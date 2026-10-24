package update

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyStartsInstallerFromTemp(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("windows"), []byte("MZ installer"), "")
	temp := t.TempDir()
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)
	rec := &recordStart{}
	orig := startDetached
	startDetached = rec.start
	t.Cleanup(func() { startDetached = orig })

	quit, err := Apply(context.Background(), client, rel)
	if !quit || err != nil {
		t.Fatalf("Apply() = %v, %v; want quit, no error", quit, err)
	}
	if len(rec.calls) != 1 || len(rec.calls[0]) != 3 {
		t.Fatalf("started %q, want one installer", rec.calls)
	}
	if dir := filepath.Dir(rec.calls[0][0]); !strings.EqualFold(filepath.Clean(dir), filepath.Clean(os.TempDir())) {
		t.Fatalf("installer in %s, want %%TEMP%% (%s)", dir, os.TempDir())
	}
}
