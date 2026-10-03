package p4

import (
	"os/exec"
	"syscall"
)

func configureP4Command(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
