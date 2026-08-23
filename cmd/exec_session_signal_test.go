//go:build !windows

package cmd

import (
	"context"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// gatedReader yields one chunk, then blocks until released. The first read
// completing is the test's proof that runExec reached its stdin goroutine,
// which is installed after signal.Notify -- so by the time we raise the
// signal, the handler that catches it is certainly in place.
//
// #78 called this test the one that could prove flaky, because a real
// os.Interrupt racing socket setup would kill the test binary rather than
// fail a case. The gate is what removes the race: nothing is raised until
// the client has demonstrably passed the point where it starts listening.
type gatedReader struct {
	once    chan string
	release chan struct{}
}

func newGatedReader(first string) *gatedReader {
	g := &gatedReader{once: make(chan string, 1), release: make(chan struct{})}
	g.once <- first
	return g
}

func (g *gatedReader) Read(p []byte) (int, error) {
	select {
	case s := <-g.once:
		return copy(p, s), nil
	default:
	}
	<-g.release
	return 0, io.EOF
}

func TestExecForwardsAnInterruptAsCtrlCWithoutTearingDownTheSession(t *testing.T) {
	// ssh and kubectl exec both treat Ctrl-C as "interrupt what is running
	// remotely", not "hang up". Go's default disposition is to die, which
	// is exit 130 with the remote's fate unstated -- the behaviour #72
	// reported from the IDE's exec terminal. This is the guard for the fix.
	var afterInterrupt execFrame
	stillAlive := false

	stdin := newGatedReader("hello\n")
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.recvUntil("stdin") // the gate: the client is now listening for signals

		if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
			t.Errorf("raising SIGINT: %v", err)
			s.send(execFrame{Type: "exit", Code: 0})
			return
		}

		afterInterrupt = s.recvUntil("stdin")
		// The session must still be usable afterwards; a client that
		// forwarded the byte and then hung up would pass a check on the
		// byte alone.
		s.send(execFrame{Type: "stdout", Data: "prompt$ "})
		s.send(execFrame{Type: "exit", Code: 0})
		stillAlive = true
	})

	withExecSession(t, stdin, &fakeTerm{cols: 80, rows: 24}, func(int) {
		t.Error("an interrupt forwarded an exit status; the session must survive it")
	})
	cmd, out, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"sh"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	close(stdin.release)

	if afterInterrupt.Data != "\x03" {
		t.Errorf("frame after the interrupt = %q, want the 0x03 byte", afterInterrupt.Data)
	}
	if !stillAlive || !strings.Contains(out.String(), "prompt$ ") {
		t.Error("the session did not survive the interrupt")
	}
}

func TestExecNonInteractiveSessionDoesNotTrapInterrupts(t *testing.T) {
	// A piped `astro exec -- cmd` in a script is expected to die on Ctrl-C
	// like any other command, so the handler is installed only for
	// interactive sessions.
	//
	// Testing a negative around signals looks unsafe -- with no handler
	// installed, raising one kills the test binary. The way out is that Go
	// delivers a signal to every registered channel: this test registers its
	// own first, which keeps the process alive whether or not runExec also
	// registered. If it did, a 0x03 stdin frame appears; if it did not,
	// nothing does. That is a real detection, not a proxy for one.
	safety := make(chan os.Signal, 1)
	signal.Notify(safety, os.Interrupt)
	defer signal.Stop(safety)

	sawCtrlC := false
	stdin := newGatedReader("q\n")
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.recvUntil("stdin") // past the point where a handler would be installed

		if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
			t.Errorf("raising SIGINT: %v", err)
			s.send(execFrame{Type: "exit", Code: 0})
			return
		}
		<-safety // the signal has been delivered; anything runExec sent is queued

		s.send(execFrame{Type: "exit", Code: 0})
		for f := range s.in {
			if f.Type == "stdin" && f.Data == "\x03" {
				sawCtrlC = true
			}
		}
	})

	withExecSession(t, stdin, notATerm{}, func(int) {})
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"cat"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	close(stdin.release)
	srv.wait(t)

	if sawCtrlC {
		t.Error("a piped session forwarded the interrupt; it must keep the default disposition and die")
	}
}
