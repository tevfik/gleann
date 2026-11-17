//go:build linux

package store

import "golang.org/x/sys/unix"

// File-system magic numbers of the network file systems SQLite's WAL mode
// cannot run on (WAL relies on shared memory). Defined here because the
// unix package does not export them.
const (
	nfsSuperMagic   = 0x6969     // NFS_SUPER_MAGIC
	smbSuperMagic   = 0x517B     // SMB_SUPER_MAGIC
	cifsMagicNumber = 0xFF534D42 // CIFS_MAGIC_NUMBER
	smb2MagicNumber = 0xFE534D42 // SMB2_MAGIC_NUMBER
)

// isNetworkFS reports whether path lives on a network file system, using
// statfs. When statfs fails the path is assumed local: detection is best
// effort (RFC 003 open question 9.9) and a wrong guess must not block opens.
func isNetworkFS(path string) bool {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false
	}
	switch uint32(st.Type) {
	case nfsSuperMagic, smbSuperMagic, cifsMagicNumber, smb2MagicNumber:
		return true
	}
	return false
}
