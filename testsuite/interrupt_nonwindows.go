//go:build !windows

package testsuite

import (
	"os"
	"syscall"
)

func sendInterrupt(process *os.Process) error {
	return process.Signal(syscall.SIGINT)
}
