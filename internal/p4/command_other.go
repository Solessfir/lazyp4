//go:build !windows

package p4

import "os/exec"

func configureP4Command(cmd *exec.Cmd) {}
