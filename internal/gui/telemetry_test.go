package gui

import (
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
)

func TestApplyTelemetry(t *testing.T) {
	c := config.Default()
	if v := settingsView(c); v.Telemetry || v.TelemetryAsked {
		t.Fatalf("default settingsView = %+v, want statistics off and not asked", v)
	}

	applyTelemetry(&c, true)
	on := c.Telemetry
	if !on.Enabled || !on.Asked || len(on.ID) != 32 {
		t.Fatalf("after turning on: %+v, want enabled, asked and an id", on)
	}
	if v := settingsView(c); !v.Telemetry || !v.TelemetryAsked {
		t.Fatalf("settingsView = %+v, want statistics on and asked", v)
	}
	applyTelemetry(&c, true)
	if c.Telemetry.ID != on.ID {
		t.Fatalf("id after turning on twice = %q, want the same %q", c.Telemetry.ID, on.ID)
	}

	applyTelemetry(&c, false)
	if off := c.Telemetry; off.Enabled || !off.Asked || off.ID != "" {
		t.Fatalf("after turning off: %+v, want off, asked and no id", off)
	}

	applyTelemetry(&c, true)
	if c.Telemetry.ID == on.ID {
		t.Fatalf("id after turning on again = %q, want a new one", c.Telemetry.ID)
	}
}

func TestSaveSettingsKeepsTelemetry(t *testing.T) {
	c := config.Default()
	applyTelemetry(&c, true)
	want := c.Telemetry
	applySettings(&c, SettingsView{Language: "de"})
	if c.Telemetry != want {
		t.Fatalf("telemetry after applySettings = %+v, want %+v", c.Telemetry, want)
	}
}
