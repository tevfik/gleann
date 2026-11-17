//go:build darwin

package store

import "golang.org/x/sys/unix"

// isNetworkFS reports whether path lives on a network file system. On darwin
// statfs reports the file-system type by name (f_fstypename), not by the
// Linux magic numbers, so the names are compared. When statfs fails the path
// is assumed local: detection is best effort (RFC 003 open question 9.9).
func isNetworkFS(path string) bool {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false
	}
	switch unix.ByteSliceToString(st.Fstypename[:]) {
	case "nfs", "smbfs", "afpfs", "webdav", "cifs":
		return true
	}
	return false
}
