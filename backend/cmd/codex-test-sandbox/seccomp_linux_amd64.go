package main

import "golang.org/x/sys/unix"

const (
	abi2SeccompArchitecture = uint32(unix.AUDIT_ARCH_X86_64)
	abi2SeccompHasOpen      = true
	abi2SeccompOpenSyscall  = uint32(unix.SYS_OPEN)
)
