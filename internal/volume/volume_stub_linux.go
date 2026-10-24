package volume

// Erase reformats the volume mounted at mountPoint once its UUID is
// wantUUID.
//
// Stub until Linux erase support lands (a later task): it always fails
// with ErrUnsupported. ListRemovable, GetInfo, FindByUUID, NewWatcher and
// Eject are implemented for Linux in volume_linux.go, watcher_linux.go,
// eject_linux.go and udisks_linux.go.
func Erase(mountPoint, wantUUID string) (Info, error) { return Info{}, ErrUnsupported }
