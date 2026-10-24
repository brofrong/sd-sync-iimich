package appimageenv

import (
	"reflect"
	"testing"
)

func TestClean(t *testing.T) {
	const appdir = "/tmp/.mount_SDSyncA1b2"
	environ := []string{
		"HOME=/home/me",
		"APPIMAGE=/home/me/Applications/SD-Sync-Immich-x86_64.AppImage",
		"APPDIR=" + appdir,
		"ARGV0=./SD-Sync-Immich-x86_64.AppImage",
		"OWD=/home/me",
		"PATH=" + appdir + "/usr/bin:/usr/bin:/bin",
		"LD_LIBRARY_PATH=" + appdir + "/usr/lib:" + appdir + "/usr/lib/x86_64-linux-gnu",
		"XDG_DATA_DIRS=" + appdir + "/usr/share:/usr/local/share:/usr/share",
		"GSETTINGS_SCHEMA_DIR=" + appdir + "/usr/share/glib-2.0/schemas",
		"GDK_PIXBUF_MODULE_FILE=" + appdir + "/usr/lib/gdk-pixbuf/loaders.cache",
		"LANG=ru_RU.UTF-8",
		"EMPTY=",
	}
	want := []string{
		"HOME=/home/me",
		"PATH=/usr/bin:/bin",
		"XDG_DATA_DIRS=/usr/local/share:/usr/share",
		"LANG=ru_RU.UTF-8",
		"EMPTY=",
	}
	if got := Clean(environ, appdir); !reflect.DeepEqual(got, want) {
		t.Fatalf("Clean = %q, want %q", got, want)
	}
}

func TestCleanWithoutAppDir(t *testing.T) {
	environ := []string{"APPIMAGE=/a.AppImage", "PATH=/usr/bin:/bin", "LD_LIBRARY_PATH=/opt/lib"}
	want := []string{"PATH=/usr/bin:/bin", "LD_LIBRARY_PATH=/opt/lib"}
	if got := Clean(environ, ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("Clean = %q, want %q", got, want)
	}
}
