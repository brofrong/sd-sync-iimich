// Package icon holds the tray icon: IdlePNG, and SyncingPNG, whose frames
// turn the ring of sync arrows around the flower. On macOS it is a
// monochrome template image (Template is true), which the menu bar tints;
// elsewhere it is the colored app icon. scripts/icons.sh renders the frames
// (tray/*.png) from build/icon.
package icon

import (
	"fmt"
	"sync"
)

// FrameCount is the number of SyncingPNG frames, 15° apart: half a turn,
// after which the ring looks the same.
const FrameCount = 12

var frames = sync.OnceValue(func() [][]byte {
	out := make([][]byte, FrameCount)
	for i := range out {
		name := fmt.Sprintf("tray/%s-%02d.png", framePrefix, i)
		b, err := frameFS.ReadFile(name)
		if err != nil {
			panic("gui icon: " + err.Error())
		}
		out[i] = b
	}
	return out
})

// IdlePNG returns the tray icon.
func IdlePNG() []byte { return frames()[0] }

// SyncingPNG returns frame (modulo FrameCount; a negative one is 0) of the
// tray icon with the ring turning.
func SyncingPNG(frame int) []byte {
	if frame < 0 {
		frame = 0
	}
	return frames()[frame%FrameCount]
}
