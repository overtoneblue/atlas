//go:build linux

package main

import "syscall"

// PR_SET_PDEATHSIG: ask the kernel to deliver SIGTERM to this process when
// its parent dies — kernel-enforced cleanup, immune to SIGKILL'd shells.
const prSetPdeathsig = 1

func dieWithParent() {
	_, _, _ = syscall.Syscall6(syscall.SYS_PRCTL, prSetPdeathsig, uintptr(syscall.SIGTERM), 0, 0, 0, 0)
}
