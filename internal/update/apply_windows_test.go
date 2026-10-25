package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestApplyStartsInstallerFromTemp(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("windows", "amd64"), []byte("MZ installer"), "")
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
	if !strings.EqualFold(filepath.Clean(rec.procs[0].dir), filepath.Clean(os.TempDir())) {
		t.Fatalf("installer runs in %q, want %%TEMP%%", rec.procs[0].dir)
	}
}

func TestStartBreaksAwayFromJob(t *testing.T) {
	const base = windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS
	tests := []struct {
		name  string
		errs  []error // what each attempt returns
		flags []uint32
		ok    bool
	}{
		{"breakaway allowed", []error{nil}, []uint32{base | windows.CREATE_BREAKAWAY_FROM_JOB}, true},
		{"job forbids breakaway", []error{&os.PathError{Op: "fork/exec", Err: windows.ERROR_ACCESS_DENIED}, nil},
			[]uint32{base | windows.CREATE_BREAKAWAY_FROM_JOB, base}, true},
		{"other error", []error{errors.New("not found")}, []uint32{base | windows.CREATE_BREAKAWAY_FROM_JOB}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var flags []uint32
			err := startWithFlags(base, func(f uint32) error {
				flags = append(flags, f)
				return tt.errs[len(flags)-1]
			})
			if (err == nil) != tt.ok {
				t.Fatalf("error = %v, want ok %v", err, tt.ok)
			}
			if len(flags) != len(tt.flags) {
				t.Fatalf("attempts with flags %#x, want %#x", flags, tt.flags)
			}
			for i := range flags {
				if flags[i] != tt.flags[i] {
					t.Fatalf("attempts with flags %#x, want %#x", flags, tt.flags)
				}
			}
		})
	}
}
