//go:build !linux && !windows

package daemon

func Install(Spec) error     { return ErrUnsupportedPlatform }
func Start(Spec) error       { return ErrUnsupportedPlatform }
func Stop(string) error      { return ErrUnsupportedPlatform }
func Restart(Spec) error     { return ErrUnsupportedPlatform }
func Uninstall(string) error { return ErrUnsupportedPlatform }
func StatusOf(string) (Status, error) {
	return Status{}, ErrUnsupportedPlatform
}
