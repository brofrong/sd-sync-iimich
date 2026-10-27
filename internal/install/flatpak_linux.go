// In a Flatpak the app cannot write the autostart entry itself: the
// Background portal writes it on the host, and the app only reads it back
// (the manifest grants xdg-config/autostart read-only).

package install

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// flatpakInfoPath exists in every Flatpak sandbox. A variable for tests.
var flatpakInfoPath = "/.flatpak-info"

// Flatpak reports whether the app runs as a Flatpak.
func Flatpak() bool {
	_, err := os.Stat(flatpakInfoPath)
	return err == nil
}

// flatpakAutostartFile is the entry the Background portal writes for this
// app, $HOME/.config/autostart/$FLATPAK_ID.desktop on the host (inside the
// sandbox $XDG_CONFIG_HOME points elsewhere).
func flatpakAutostartFile() (string, error) {
	id := os.Getenv("FLATPAK_ID")
	if id == "" || strings.ContainsAny(id, "/\x00") {
		return "", errors.New("FLATPAK_ID is not set")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "autostart", id+".desktop"), nil
}

// flatpakAutostartEnabled reports whether the portal's entry exists and is
// not turned off (see AutostartEnabled).
func flatpakAutostartEnabled() bool {
	path, err := flatpakAutostartFile()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return entryOn(desktopEntryKeys(string(data)))
}

// entryOn reports whether an autostart entry's keys leave it on: not
// Hidden, and not turned off by the GNOME or MATE session settings.
func entryOn(keys map[string]string) bool {
	return !strings.EqualFold(keys["Hidden"], "true") &&
		!strings.EqualFold(keys["X-GNOME-Autostart-enabled"], "false") &&
		!strings.EqualFold(keys["X-MATE-Autostart-enabled"], "false")
}

// flatpakCommandline is what the portal's entry runs, inside the sandbox.
var flatpakCommandline = []string{"sd-sync-immich", "run", "--autostart"}

// portalTimeout bounds the wait for the portal's answer, which may wait
// for the user to allow the app to run in the background.
const portalTimeout = 2 * time.Minute

// requestBackgroundFn asks the Background portal to start the app at login
// or not. A variable so tests never talk to a real portal.
var requestBackgroundFn = requestBackground

// requestBackground calls org.freedesktop.portal.Background.RequestBackground
// and waits for its Response: 0 is success, and with autostart asked for,
// the results must grant it.
func requestBackground(autostart bool) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("background portal: %w", err)
	}
	defer conn.Close()

	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	token := "sdsync" + hex.EncodeToString(b)
	sender := strings.ReplaceAll(strings.TrimPrefix(conn.Names()[0], ":"), ".", "_")
	handle := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + token)
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(handle),
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return fmt.Errorf("background portal: %w", err)
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)

	options := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"reason":       dbus.MakeVariant("Upload new photos as soon as a camera card is inserted"),
		"autostart":    dbus.MakeVariant(autostart),
		"commandline":  dbus.MakeVariant(flatpakCommandline),
	}
	portal := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	if err := portal.Call("org.freedesktop.portal.Background.RequestBackground", 0, "", options).Err; err != nil {
		return fmt.Errorf("background portal: %w", err)
	}

	timeout := time.After(portalTimeout)
	for {
		select {
		case sig := <-signals:
			if sig.Path != handle || len(sig.Body) < 2 {
				continue
			}
			response, _ := sig.Body[0].(uint32)
			results, _ := sig.Body[1].(map[string]dbus.Variant)
			return backgroundResult(autostart, response, results)
		case <-timeout:
			return errors.New("background portal: no answer")
		}
	}
}

// backgroundResult turns the portal's Response into an error: response 1
// means the user said no, 2 that the request failed; starting at login is
// granted only when the results say autostart is true.
func backgroundResult(autostart bool, response uint32, results map[string]dbus.Variant) error {
	if response != 0 {
		return fmt.Errorf("background portal: request refused (response %d)", response)
	}
	if !autostart {
		return nil
	}
	if v, ok := results["autostart"]; ok {
		if granted, _ := v.Value().(bool); granted {
			return nil
		}
	}
	return errors.New("background portal: starting at login was not allowed")
}
