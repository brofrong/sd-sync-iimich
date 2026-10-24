// Package appimageenv cleans the environment the AppImage runtime and its
// AppRun set up for the app before the app starts another program: the
// AppImage's bundled libraries, binaries and data must not reach a
// helper (xdg-open and the browser it starts) or the next version of the
// app.
package appimageenv

import "strings"

// Clean is environ without what the AppImage runtime and its AppRun set:
// $APPIMAGE, $APPDIR, $ARGV0, $OWD, and every path into appdir, the
// AppImage's mount. A list of paths (PATH, LD_LIBRARY_PATH,
// XDG_DATA_DIRS, ...) loses those entries; a variable left empty, or a
// single path into appdir (a GTK or GIO module file, say), is dropped.
// With appdir "" only the four variables go.
func Clean(environ []string, appdir string) []string {
	env := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, value, _ := strings.Cut(kv, "=")
		switch {
		case name == "APPIMAGE" || name == "APPDIR" || name == "ARGV0" || name == "OWD":
			continue
		case appdir != "" && strings.Contains(value, appdir):
			var kept []string
			for _, entry := range strings.Split(value, ":") {
				if !strings.Contains(entry, appdir) {
					kept = append(kept, entry)
				}
			}
			if len(kept) == 0 {
				continue
			}
			kv = name + "=" + strings.Join(kept, ":")
		}
		env = append(env, kv)
	}
	return env
}
