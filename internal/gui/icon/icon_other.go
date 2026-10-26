//go:build !darwin

package icon

import "embed"

// Template is false: the icon is the colored app icon, shown as is.
const Template = false

//go:embed tray/color-*.png
var frameFS embed.FS

const framePrefix = "color"
