//go:build !linux

package main

// dieWithParent is a no-op off Linux (PR_SET_PDEATHSIG is Linux-only).
func dieWithParent() {}
