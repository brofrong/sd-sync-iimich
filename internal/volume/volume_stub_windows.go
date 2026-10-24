package volume

// Erase reformats the volume mounted at mountPoint once its UUID is
// wantUUID.
//
// Stub until Windows erase support lands (a later task): it always fails
// with ErrUnsupported. ListRemovable, GetInfo, FindByUUID, NewWatcher and
// Eject are implemented for Windows in volume_windows.go, watcher_windows.go
// and eject_windows.go.
func Erase(mountPoint, wantUUID string) (Info, error) { return Info{}, ErrUnsupported }
