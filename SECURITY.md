# Security

## Reporting a problem

Please report security problems privately: on the repository's
**Security** tab, click
[**Report a vulnerability**](https://github.com/brofrong/sd-sync-immich/security/advisories/new).
Do not open a public issue for them. You will get an answer as soon as I
can, and a fix goes out in a new release.

Only the latest release is supported. The app updates itself, so fixes
reach everyone who keeps updates on.

## What the app does with your data

- **API key.** Stored in plain text in `config.yaml` in your user's settings
  folder (`~/.config/sd-sync-immich` on macOS and Linux,
  `%APPDATA%\sd-sync-immich` on Windows). On macOS and Linux the file is
  readable only by you. The window never gets the saved key back, the key is
  never written to the log, and it is only sent to the server it was saved
  for: testing another address needs that server's key.
- **Network.** The app talks to your Immich server and to GitHub: the
  GitHub API to check for a new version, and the release page to download
  it. Nothing else, unless you allow anonymous statistics.
- **Anonymous statistics.** Off until you allow them (the app asks once;
  **Settings → Statistics**). When on, the app sends to
  `sd-sync-stats.brofrong.ru`: a random id made when you turn them on (a
  new one each time), the app's version, the system and its version, the
  CPU architecture, how the app is installed and its language; that it
  started and, once a day, that it still runs; each sync's counts (files,
  uploaded, duplicates, failed by kind, bytes, duration) and outcome; and
  the messages of errors it logs, without their details. Never file names,
  paths, card names, the server's address or the API key. The server keeps
  no IP addresses and deletes events after 400 days. Its code is in
  [`internal/telemetryserver`](internal/telemetryserver), the app's side in
  [`internal/telemetry`](internal/telemetry).
- **Updates.** A downloaded update is checked against the SHA-256 in the
  release's `checksums.txt` before it replaces the app. The builds are not
  signed with an Apple or Microsoft certificate.
- **Formatting the card.** Off by default. When turned on, the card is
  formatted only after the app has checked that every photo and video on it
  is in Immich, with a 30-second countdown that can be cancelled. Formatting
  erases everything on the card.
