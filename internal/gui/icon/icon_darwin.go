package icon

import "embed"

// Template is true: the icon is a macOS menu bar template image (black
// and alpha), which the menu bar tints to match its appearance.
const Template = true

//go:embed tray/template-*.png
var frameFS embed.FS

const framePrefix = "template"
