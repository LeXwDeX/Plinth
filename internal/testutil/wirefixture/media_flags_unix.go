//go:build darwin || linux

package wirefixture

import "syscall"

const (
	noFollow = syscall.O_NOFOLLOW
	nonBlock = syscall.O_NONBLOCK
)
