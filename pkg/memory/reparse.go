package memory

import "os"

// fileAttributeReparsePoint is the Windows FILE_ATTRIBUTE_REPARSE_POINT bit.
// Directory junctions (mklink /J) carry it, and Go's Lstat does not report
// them as symlinks.
const fileAttributeReparsePoint = 0x400

// isLinkLike reports whether a file with this mode and Windows attribute word
// is a symlink, a junction or any other reparse point. Export pruning refuses
// all of them: only a plain directory or a regular file may be traversed or
// deleted. attrs is zero on platforms without file attributes.
func isLinkLike(mode os.FileMode, attrs uint32) bool {
	return mode&(os.ModeSymlink|os.ModeIrregular) != 0 || attrs&fileAttributeReparsePoint != 0
}

// fileIsLinkLike is isLinkLike applied to an Lstat result.
func fileIsLinkLike(fi os.FileInfo) bool {
	return isLinkLike(fi.Mode(), fileAttributes(fi))
}
