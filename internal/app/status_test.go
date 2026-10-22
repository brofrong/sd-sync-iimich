package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

func equalActions(a, b []Action) bool {
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

func configuredConfig() config.Config {
	return config.Config{
		Immich: config.ImmichConfig{URL: "https://immich.example", APIKey: "key"},
	}
}

func TestIdleStatus(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		mounted     []volume.Info
		wantOK      bool
		wantCode    Code
		wantParams  map[string]string
		wantActions []Action
	}{
		{
			name:        "empty url is not configured",
			cfg:         config.Config{},
			wantOK:      true,
			wantCode:    CodeNotConfigured,
			wantActions: []Action{ActionOpenServer},
		},
		{
			name:        "server configured but no card selected",
			cfg:         configuredConfig(),
			wantOK:      true,
			wantCode:    CodeNoCardSelected,
			wantActions: []Action{ActionOpenCards},
		},
		{
			name: "selected card not mounted",
			cfg: func() config.Config {
				c := configuredConfig()
				c.Watch = config.WatchConfig{VolumeUUID: "A", VolumeName: "Brofrong"}
				return c
			}(),
			wantOK:     true,
			wantCode:   CodeWaitingForCard,
			wantParams: map[string]string{"name": "Brofrong"},
		},
		{
			name: "different card mounted",
			cfg: func() config.Config {
				c := configuredConfig()
				c.Watch = config.WatchConfig{VolumeUUID: "A", VolumeName: "Brofrong"}
				return c
			}(),
			mounted:  []volume.Info{{UUID: "B", Name: "Brofrong"}},
			wantOK:   true,
			wantCode: CodeOtherCardMounted,
			wantParams: map[string]string{
				"selected_name": "Brofrong",
				"mounted_name":  "Brofrong",
				"mounted_uuid":  "B",
			},
			wantActions: []Action{ActionSelectCard},
		},
		{
			name: "selected card is mounted",
			cfg: func() config.Config {
				c := configuredConfig()
				c.Watch = config.WatchConfig{VolumeUUID: "A", VolumeName: "Brofrong"}
				return c
			}(),
			mounted: []volume.Info{{UUID: "A", Name: "Brofrong"}},
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := IdleStatus(tt.cfg, tt.mounted)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.Code != tt.wantCode {
				t.Fatalf("Code = %q, want %q", got.Code, tt.wantCode)
			}
			for k, v := range tt.wantParams {
				if got.Params[k] != v {
					t.Errorf("Params[%q] = %q, want %q (all params: %+v)", k, got.Params[k], v, got.Params)
				}
			}
			if tt.wantActions != nil && !equalActions(got.Actions, tt.wantActions) {
				t.Errorf("Actions = %v, want %v", got.Actions, tt.wantActions)
			}
		})
	}
}

func TestResultStatus(t *testing.T) {
	tests := []struct {
		name        string
		res         uploader.Result
		err         error
		cardRemoved bool
		serverHost  string
		wantCode    Code
		wantParams  map[string]string
		wantActions []Action
	}{
		{
			name:     "nothing new to upload",
			res:      uploader.Result{Uploaded: 0, Skipped: 10},
			wantCode: CodeNothingNew,
		},
		{
			name:        "done",
			res:         uploader.Result{Uploaded: 5},
			wantCode:    CodeDone,
			wantParams:  map[string]string{"uploaded": "5"},
			wantActions: []Action{ActionEject, ActionOpenImmich},
		},
		{
			name:        "done with errors",
			res:         uploader.Result{Uploaded: 5, Errors: 2, ErrorSummary: map[string]int{"rejected": 2}},
			wantCode:    CodeDoneWithErrors,
			wantParams:  map[string]string{"uploaded": "5", "errors": "2"},
			wantActions: []Action{ActionOpenLogs},
		},
		{
			name:        "server unreachable",
			err:         fmt.Errorf("%w: %w", uploader.ErrServerUnavailable, &net.DNSError{Err: "no such host"}),
			serverHost:  "immich.example",
			wantCode:    CodeServerUnreachable,
			wantParams:  map[string]string{"host": "immich.example", "detail": "no such host"},
			wantActions: []Action{ActionRetry},
		},
		{
			name:        "server error",
			err:         fmt.Errorf("%w: %w", uploader.ErrServerUnavailable, &immich.StatusError{Code: 502, Message: "bad gateway"}),
			wantCode:    CodeServerError,
			wantParams:  map[string]string{"status": "502"},
			wantActions: []Action{ActionRetry},
		},
		{
			name:        "auth failed",
			err:         fmt.Errorf("upload: %w", uploader.ErrAuthFailed),
			wantCode:    CodeAuthFailed,
			wantActions: []Action{ActionOpenServer},
		},
		{
			name:     "cancelled by user",
			err:      context.Canceled,
			wantCode: CodeCancelled,
		},
		{
			name:        "cancelled because card was removed",
			err:         context.Canceled,
			cardRemoved: true,
			wantCode:    CodeCardRemoved,
		},
		{
			name:        "generic failure",
			err:         errors.New("boom"),
			wantCode:    CodeFailed,
			wantActions: []Action{ActionOpenLogs},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResultStatus(tt.res, tt.err, tt.cardRemoved, tt.serverHost)
			if got.Code != tt.wantCode {
				t.Fatalf("Code = %q, want %q", got.Code, tt.wantCode)
			}
			for k, v := range tt.wantParams {
				if got.Params[k] != v {
					t.Errorf("Params[%q] = %q, want %q (all params: %+v)", k, got.Params[k], v, got.Params)
				}
			}
			if tt.wantActions != nil && !equalActions(got.Actions, tt.wantActions) {
				t.Errorf("Actions = %v, want %v", got.Actions, tt.wantActions)
			}
		})
	}
}
