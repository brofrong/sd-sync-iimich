package gui

import (
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
)

func TestSettingsConcurrency(t *testing.T) {
	cfg := config.Default()
	if got := settingsConcurrency(cfg); got != 0 {
		t.Fatalf("settingsConcurrency(auto) = %d, want 0", got)
	}
	cfg.Upload.Concurrency = 10
	if got := settingsConcurrency(cfg); got != 10 {
		t.Fatalf("settingsConcurrency(10) = %d, want 10", got)
	}
	cfg.Upload.Concurrency = 40 // edited by hand
	if got := settingsConcurrency(cfg); got != config.MaxConcurrency {
		t.Fatalf("settingsConcurrency(40) = %d, want %d", got, config.MaxConcurrency)
	}
}

func TestValidConcurrency(t *testing.T) {
	for n, want := range map[int]bool{-1: false, 0: true, 1: true, 16: true, 17: false} {
		if got := validConcurrency(n); got != want {
			t.Errorf("validConcurrency(%d) = %v, want %v", n, got, want)
		}
	}
}
