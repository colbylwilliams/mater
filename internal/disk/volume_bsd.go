//go:build darwin || freebsd

package disk

import "syscall"

func statVolume(path string) (Volume, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Volume{}, err
	}
	block := int64(st.Bsize)
	return Volume{
		Mount: cstring(st.Mntonname[:]),
		// FreeBSD lets root write into the reserve, which drives the count
		// available to everyone else below zero.
		Free: max(int64(st.Bavail), 0) * block,
		Size: int64(st.Blocks) * block,
	}, nil
}

// cstring reads a NUL-terminated name out of a fixed-size kernel buffer.
func cstring(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
