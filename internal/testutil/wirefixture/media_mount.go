package wirefixture

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

type mountRecord struct {
	root       string
	mountPoint string
}

func parseMountInfo(data []byte) ([]mountRecord, error) {
	var records []mountRecord
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		fields := strings.Fields(string(line))
		if len(fields) < 7 {
			return nil, fmt.Errorf("%w: mount information", ErrMediaEvidence)
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 5 {
			return nil, fmt.Errorf("%w: mount information", ErrMediaEvidence)
		}
		root, err := unescapeMountPath(fields[3])
		if err != nil {
			return nil, err
		}
		mountPoint, err := unescapeMountPath(fields[4])
		if err != nil {
			return nil, err
		}
		records = append(records, mountRecord{root: root, mountPoint: mountPoint})
	}
	return records, nil
}

func unescapeMountPath(value string) (string, error) {
	if !strings.Contains(value, "\\") {
		return value, nil
	}
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			out.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", fmt.Errorf("%w: mount escape", ErrMediaEvidence)
		}
		code, err := strconv.ParseUint(value[i+1:i+4], 8, 8)
		if err != nil {
			return "", fmt.Errorf("%w: mount escape", ErrMediaEvidence)
		}
		switch code {
		case 0o040, 0o011, 0o012, 0o134:
			out.WriteByte(byte(code))
		default:
			return "", fmt.Errorf("%w: mount escape", ErrMediaEvidence)
		}
		i += 3
	}
	return out.String(), nil
}

func rejectBindMountOverlap(records []mountRecord, root, dir string) error {
	for _, record := range records {
		if record.root == "/" {
			continue
		}
		if inside(record.mountPoint, root) || inside(record.mountPoint, dir) {
			return fmt.Errorf("%w: bind mount alias", ErrInsideWorktree)
		}
	}
	return nil
}
