package main

import "golang.org/x/sys/unix"

const (
	abi2SeccompArchitecture = uint32(unix.AUDIT_ARCH_AARCH64)
	abi2SeccompHasOpen      = false
	abi2SeccompOpenSyscall  = uint32(0)
)
