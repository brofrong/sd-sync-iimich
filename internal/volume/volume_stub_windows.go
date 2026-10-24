package volume

// Stubs until this system's volume support lands: every call fails with
// ErrUnsupported.

// ListRemovable lists the mounted removable volumes.
func ListRemovable() ([]Info, error) { return nil, ErrUnsupported }

// GetInfo describes the volume mounted at mountPoint.
func GetInfo(mountPoint string) (*Info, error) { return nil, ErrUnsupported }

// FindByUUID returns the mounted volume with uuid, or nil if none is.
func FindByUUID(uuid string) (*Info, error) { return nil, ErrUnsupported }

// NewWatcher starts watching volumes being mounted and unmounted.
func NewWatcher() (*Watcher, error) { return nil, ErrUnsupported }

// Eject ejects the volume mounted at mountPoint.
func Eject(mountPoint string) error { return ErrUnsupported }

// Erase reformats the volume mounted at mountPoint once its UUID is
// wantUUID.
func Erase(mountPoint, wantUUID string) (Info, error) { return Info{}, ErrUnsupported }
