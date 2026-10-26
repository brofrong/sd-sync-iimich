# dmgbuild settings for SD-Sync-Immich.dmg (see scripts/build-app.sh): the
# app and a link to /Applications side by side over background.tiff, with
# an arrow between them. Both defines are required:
#   -D app=<path to SD Sync Immich.app>
#   -D background=<path to background.tiff>
#
# The icon positions are the centres the background's arrow and discs are
# drawn around (build/darwin/dmg/background.html, 660 × 400 points).
import os.path

app = defines["app"]  # noqa: F821 (dmgbuild provides defines)
app_name = os.path.basename(app)

format = "ULFO"
filesystem = "HFS+"

files = [app]
symlinks = {"Applications": "/Applications"}
# No hide_extensions: it marks the bundle with com.apple.FinderInfo, which
# codesign --strict rejects (Finder hides ".app" anyway).

# The volume shows the app's icon.
icon = os.path.join(app, "Contents", "Resources", "icons.icns")
background = defines["background"]  # noqa: F821

default_view = "icon-view"
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
# The bounds include the 32-point title bar above the 660 × 400 background.
window_rect = ((200, 140), (660, 432))

icon_size = 128
text_size = 13
icon_locations = {
    app_name: (165, 200),
    "Applications": (495, 200),
}
