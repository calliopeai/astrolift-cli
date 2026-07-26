//go:build windows

package cmd

// watchResize is a no-op on Windows: there is no SIGWINCH, and console resize
// events are not delivered as OS signals. The initial size is still sent once
// by the caller; only live resize propagation is unavailable.
func watchResize(_ func()) (stop func()) {
	return func() {}
}
