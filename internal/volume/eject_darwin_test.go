//go:build darwin

package volume

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestEjectRunsDiskutil(t *testing.T) {
	var gotArgs []string
	orig := runDiskutil
	defer func() { runDiskutil = orig }()
	runDiskutil = func(args ...string) ([]byte, error) {
		gotArgs = args
		return nil, nil
	}

	if err := Eject("/Volumes/X"); err != nil {
		t.Fatalf("Eject() error = %v", err)
	}

	want := []string{"eject", "/Volumes/X"}
	if !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("runDiskutil args = %v, want %v", gotArgs, want)
	}
}

func TestEjectReturnsCommandOutputOnError(t *testing.T) {
	orig := runDiskutil
	defer func() { runDiskutil = orig }()
	runDiskutil = func(args ...string) ([]byte, error) {
		return []byte("Disk busy"), errors.New("exit status 1")
	}

	err := Eject("/Volumes/X")
	if err == nil {
		t.Fatal("Eject() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "Disk busy") {
		t.Fatalf("Eject() error = %q, want it to contain %q", err.Error(), "Disk busy")
	}
}
