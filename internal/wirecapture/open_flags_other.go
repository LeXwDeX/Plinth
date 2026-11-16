//go:build !darwin && !linux

package wirecapture

// noFollow is unavailable on this platform; the directory checks still apply.
const noFollow = 0
