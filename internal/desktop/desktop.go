// Package desktop hands URLs, files and folders to the system's desktop:
// OpenURL opens a URL in the default browser, Open opens a file or folder
// with its default app, Reveal shows a file in the file manager. Each
// system's file implements them through a seam the tests replace, so no
// test ever launches anything.
package desktop
