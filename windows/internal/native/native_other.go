//go:build !windows

package native

func Load(dir string) error                                    { return ErrUnsupported }
func Loaded() bool                                             { return false }
func VirtualCameraSupported() bool                             { return false }
func StartVirtualCamera(name string) error                     { return ErrUnsupported }
func StopVirtualCamera()                                       {}
func Register() error                                          { return ErrUnsupported }
func Unregister() error                                        { return ErrUnsupported }
func NewDecoder(codec int) (Decoder, error)                    { return nil, ErrUnsupported }
func OpenAudio(names []string, rate, ch int) (AudioOut, error) { return nil, ErrUnsupported }
