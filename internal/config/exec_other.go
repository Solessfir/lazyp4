//go:build !windows

package config

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
