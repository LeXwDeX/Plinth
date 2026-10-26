//go:build !linux

package wirefixture

func rejectBindMountAliases(root, dir string) error {
	return nil
}
