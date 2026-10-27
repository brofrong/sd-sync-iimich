# Contributing

Thanks for wanting to help! Issues and pull requests are welcome, in English
or Russian.

## Ways to help

- **Report a bug.** [Open an issue](https://github.com/brofrong/sd-sync-iimich/issues/new/choose)
  with your system, the app version (top of Settings) and `app.log`
  (Settings → Open Log).
- **Try the betas.** Windows and Linux builds are new: a report that
  something works or breaks on your machine, camera or desktop helps a lot.
- **Translate.** The texts live in [`frontend/locales/`](frontend/locales),
  one JSON file per language, shared by the window and the Go side
  (notifications, tray menu). Fixing a clumsy phrase is a fine first PR.
- **Send code.** For anything bigger than a small fix, open an issue first
  so we can agree on the approach.

## Getting started

You need [Go](https://go.dev/dl/) 1.26+ and [Node.js](https://nodejs.org)
22+. On macOS also Xcode Command Line Tools (`xcode-select --install`), on
Linux the GTK 3 and WebKitGTK 4.1 headers (`libgtk-3-dev
libwebkit2gtk-4.1-dev` on Debian and Ubuntu) and `-tags=gtk3`. The Wails CLI
runs through `go run`, nothing to install.

```bash
git clone https://github.com/brofrong/sd-sync-iimich.git
cd sd-sync-iimich
scripts/build-frontend.sh          # npm ci, TypeScript bindings, frontend/dist
go build -o sd-sync-immich ./cmd/sd-sync-immich
```

### The window in a browser

The UI can be worked on without a card or an Immich server: a mock
backend replays a sync and answers every call.

```bash
cd frontend
VITE_MOCK=1 npx vite
```

Open the printed address. URL parameters pin a state, a section, a language
and so on, for example `/?section=gallery&state=done&lang=de`; the list is at
the top of [`frontend/src/lib/mock.ts`](frontend/src/lib/mock.ts).

### Checks

The same as CI runs on macOS, Windows and Linux:

```bash
go vet ./...
go test ./...
cd frontend && npm run check
```

`scripts/test-linux.sh` runs the Go checks for Linux in Docker from a Mac.

## Where things are

| | |
|---|---|
| `cmd/sd-sync-immich` | Entry point and command-line commands |
| `internal/app` | The sync loop and the statuses the window shows |
| `internal/volume` | Finding, watching, ejecting and formatting cards |
| `internal/scanner` | Finding photos and videos on a card |
| `internal/uploader`, `internal/immich` | Uploading and the Immich API client |
| `internal/state` | What each card has already uploaded (SQLite) |
| `internal/gallery` | The Gallery tab: listing photos, thumbnails |
| `internal/gui` | Wails: tray, window, the services the frontend calls |
| `internal/update` | Checking for and installing new versions |
| `frontend/src` | The Svelte 5 window |
| `site/` | The landing page on GitHub Pages |

More details (config, how updates work per system, building release files)
are in [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) (in Russian).

## Ground rules

- **Tests never touch real cards, disks or your settings.** They use
  temporary folders and `testenv.Home`, and fakes instead of ejecting or
  formatting anything. Please keep it that way: formatting is destructive.
- **New texts go in every locale file.** If you do not speak a language, put
  the English text there; the tests check that every key the code uses
  exists.
- **Commit messages** follow the existing style: `feat: …`, `fix(gallery): …`,
  `docs: …`, with a body that says why.

Releases are made by the maintainer.
