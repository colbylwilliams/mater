//go:build linux

package disk

import "syscall"

func statVolume(path string) (Volume, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Volume{}, err
	}
	// Block counts are in fragment-size units, which is what df multiplies by;
	// f_bsize is only the preferred transfer size. Kernels that predate
	// f_frsize leave it zero.
	block := int64(st.Frsize)
	if block == 0 {
		block = int64(st.Bsize)
	}
	return Volume{
		Mount: mountPoint(path),
		Free:  int64(st.Bavail) * block,
		Size:  int64(st.Blocks) * block,
	}, nil
}
