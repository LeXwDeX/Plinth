//go:build darwin || linux

package wirecapture

import "syscall"

// noFollow keeps every file the package creates from following a symbolic link.
const noFollow = syscall.O_NOFOLLOW
