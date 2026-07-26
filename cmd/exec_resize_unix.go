//go:build !windows

package cmd

import (
	"os"
	"os/signal"
	"syscall"
)

// watchResize calls sendResize on every terminal-resize (SIGWINCH) signal and
// returns a stop func that deregisters the handler. Unix-only — SIGWINCH does
// not exist on Windows, which uses the no-op in exec_resize_windows.go.
func watchResize(sendResize func()) (stop func()) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		for range winch {
			sendResize()
		}
	}()
	return func() { signal.Stop(winch) }
}
