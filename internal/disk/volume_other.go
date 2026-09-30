//go:build !darwin && !freebsd && !linux

package disk

import "errors"

// statVolume has no implementation here, so Volumes reports nothing and
// callers simply omit free space.
func statVolume(string) (Volume, error) { return Volume{}, errors.ErrUnsupported }
