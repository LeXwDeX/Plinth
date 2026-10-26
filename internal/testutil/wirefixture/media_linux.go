//go:build linux

package wirefixture

import (
	"fmt"
	"os"
)

func rejectBindMountAliases(root, dir string) error {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return fmt.Errorf("%w: mount information unavailable", ErrMediaEvidence)
	}
	records, err := parseMountInfo(data)
	if err != nil {
		return err
	}
	return rejectBindMountOverlap(records, root, dir)
}
