package gui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
)

func TestSaveSettingsCardAndNotifications(t *testing.T) {
	c := config.Default()
	v := SettingsView{
		Language:    "de",
		Concurrency: 4,
		AutoPaths:   false,
		AutoEject:   false,
		AutoFormat:  true,
		Notifications: NotificationsView{
			Enabled:   false,
			Done:      true,
			Errors:    false,
			OtherCard: true,
			Server:    false,
			Card:      true,
		},
	}
	applySettings(&c, v)

	if c.UI.Language != "de" || c.Upload.Concurrency != 4 || c.Scan.AutoPaths {
		t.Errorf("general settings = %q/%d/%v, want de/4/false", c.UI.Language, c.Upload.Concurrency, c.Scan.AutoPaths)
	}
	if want := (config.CardConfig{AutoEject: false, AutoFormat: true}); c.Card != want {
		t.Errorf("Card = %+v, want %+v", c.Card, want)
	}
	want := config.NotifyConfig{Enabled: false, Done: true, Errors: false, OtherCard: true, Server: false, Card: true}
	if c.Notifications != want {
		t.Errorf("Notifications = %+v, want %+v", c.Notifications, want)
	}
}

func TestSettingsViewRoundTrip(t *testing.T) {
	c := config.Default()
	c.Card = config.CardConfig{AutoEject: false, AutoFormat: true}
	c.Notifications = config.NotifyConfig{Enabled: true, Done: false, Errors: true, OtherCard: false, Server: true, Card: false}
	v := settingsView(c)
	if !v.AutoFormat || v.AutoEject {
		t.Errorf("settingsView card = %v/%v, want eject false, format true", v.AutoEject, v.AutoFormat)
	}
	got := config.Default()
	applySettings(&got, v)
	if got.Card != c.Card || got.Notifications != c.Notifications {
		t.Errorf("round trip = %+v %+v, want %+v %+v", got.Card, got.Notifications, c.Card, c.Notifications)
	}
}

func TestRetryError(t *testing.T) {
	if err := retryError(nil); err != nil {
		t.Fatalf("retryError(nil) = %v, want nil", err)
	}
	err := retryError(fmt.Errorf("wrapped: %w", app.ErrRetryUnavailable))
	if err == nil || err.Error() != errRetryUnavailable {
		t.Fatalf("retryError(unavailable) = %v, want %q", err, errRetryUnavailable)
	}
	if !errors.Is(err, app.ErrRetryUnavailable) {
		t.Errorf("retryError(unavailable) does not wrap app.ErrRetryUnavailable")
	}
	other := errors.New("disk on fire")
	if err := retryError(other); err == nil || err.Error() == errRetryUnavailable {
		t.Errorf("retryError(other) = %v, want a plain error", err)
	}
}
