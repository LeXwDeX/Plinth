package diagbundle

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var daysRE = regexp.MustCompile(`^(\d+)d$`)

// ParseSince reads the start of the time range: a duration back from now (Go
// syntax such as 90m or 24h, or whole days such as 7d) or an RFC 3339 instant.
func ParseSince(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("时间范围不能为空")
	}
	if match := daysRE.FindStringSubmatch(value); match != nil {
		days, err := strconv.Atoi(match[1])
		if err != nil || days <= 0 || days > 3650 {
			return time.Time{}, fmt.Errorf("天数无效: %q", value)
		}
		return now.UTC().AddDate(0, 0, -days), nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		if duration <= 0 {
			return time.Time{}, fmt.Errorf("时间范围必须为正: %q", value)
		}
		return now.UTC().Add(-duration), nil
	}
	if at, err := time.Parse(time.RFC3339, value); err == nil {
		if !at.Before(now) {
			return time.Time{}, fmt.Errorf("时间点必须早于现在: %q", value)
		}
		return at.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("无法读取时间范围 %q：用 24h、90m、7d 这样的时长，或 RFC 3339 时间点", value)
}

var byteSizeRE = regexp.MustCompile(`(?i)^(\d+)\s*([kmg]i?b?)?$`)

// ParseByteSize reads a size in bytes with an optional binary suffix (k, m, g;
// 64m is 64 MiB).
func ParseByteSize(value string) (int64, error) {
	match := byteSizeRE.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return 0, fmt.Errorf("无法读取大小 %q：写成字节数，或带 k、m、g 后缀（如 64m）", value)
	}
	number, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("大小过大: %q", value)
	}
	unit := int64(1)
	switch strings.ToLower(match[2][:min(1, len(match[2]))]) {
	case "k":
		unit = 1 << 10
	case "m":
		unit = 1 << 20
	case "g":
		unit = 1 << 30
	}
	if number > MaxMaxBytes/unit {
		return 0, fmt.Errorf("大小过大: %q", value)
	}
	return number * unit, nil
}
