package power

import "github.com/dmitiigorshkov/sd-sync-immich/internal/logx"

// KeepAwake is a stub until this system's support lands: it only logs, and
// release does nothing.
func KeepAwake() (release func()) {
	logx.Info("power: keeping the system awake is not supported on this system yet")
	return func() {}
}
