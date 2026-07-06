package app

import (
	"fmt"
	"time"
)

// FormatETA formats remaining duration for systray display.
func FormatETA(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	d = d.Round(time.Second)
	sec := int(d.Seconds())
	if sec < 60 {
		return fmt.Sprintf("~%d сек", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("~%d мин", sec/60)
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	if m == 0 {
		return fmt.Sprintf("~%d ч", h)
	}
	return fmt.Sprintf("~%d ч %d мин", h, m)
}
