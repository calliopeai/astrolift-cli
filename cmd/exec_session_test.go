package cmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// Session behaviours of `astro exec` (#78). Harness in exec_harness_test.go.

func TestExecOpenFrameCarriesCommandContainerAndTTY(t *testing.T) {
	var open execFrame
	srv := newFakeRelay(t, func(s *relaySession) {
		open = s.recvUntil("open")
		s.send(execFrame{Type: "exit", Code: 0})
	})

	withExecSession(t, newBlockingReader(t), &fakeTerm{cols: 120, rows: 40}, func(int) {
		t.Fatal("exit 0 must not forward a status")
	})
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"psql", "-c", "select 1"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)

	if got := strings.Join(open.Command, " "); got != "psql -c select 1" {
		t.Errorf("open command = %q, want the argv as given", got)
	}
	if open.Container != "app" {
		t.Errorf("open container = %q, want %q", open.Container, "app")
	}
	// A PTY is requested only for interactive sessions; a piped run must not
	// get one or output comes back echo-doubled and CRLF-mangled.
	if !open.TTY {
		t.Error("interactive session did not request a tty")
	}
}

func TestExecDefaultsToShellWhenNoCommandGiven(t *testing.T) {
	var open execFrame
	srv := newFakeRelay(t, func(s *relaySession) {
		open = s.recvUntil("open")
		s.send(execFrame{Type: "exit", Code: 0})
	})

	withExecSession(t, newBlockingReader(t), &fakeTerm{cols: 80, rows: 24}, func(int) {})
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), nil); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if len(open.Command) != 1 || open.Command[0] != "sh" {
		t.Errorf("open command = %v, want [sh]", open.Command)
	}
}

func TestExecSendsInitialResizeWithTerminalSize(t *testing.T) {
	var resize execFrame
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		resize = s.recvUntil("resize")
		s.send(execFrame{Type: "exit", Code: 0})
	})

	withExecSession(t, newBlockingReader(t), &fakeTerm{cols: 203, rows: 51}, func(int) {})
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"sh"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	// term.GetSize returns (cols, rows) and the frame is keyed rows/cols;
	// crossing them is the easy mistake and it silently halves usable width.
	if resize.Rows != 51 || resize.Cols != 203 {
		t.Errorf("resize = rows %d cols %d, want rows 51 cols 203", resize.Rows, resize.Cols)
	}
}

func TestExecNonInteractiveSessionAsksForNoTTYAndNoResize(t *testing.T) {
	var open execFrame
	sawResize := false
	srv := newFakeRelay(t, func(s *relaySession) {
		open = s.recvUntil("open")
		s.send(execFrame{Type: "exit", Code: 0})
		for f := range s.in {
			if f.Type == "resize" {
				sawResize = true
			}
		}
	})

	withExecSession(t, newBlockingReader(t), notATerm{}, func(int) {})
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"cat"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if open.TTY {
		t.Error("piped session requested a tty")
	}
	if sawResize {
		t.Error("piped session sent a resize frame")
	}
}

func TestExecForwardsStdinAsStdinFrames(t *testing.T) {
	var got execFrame
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		got = s.recvUntil("stdin")
		s.send(execFrame{Type: "exit", Code: 0})
	})

	withExecSession(t, strings.NewReader("select 1;\n"), notATerm{}, func(int) {})
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"psql"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if got.Data != "select 1;\n" {
		t.Errorf("stdin data = %q, want the bytes as read", got.Data)
	}
}

func TestExecStdinEOFHalfClosesRatherThanClosingTheSession(t *testing.T) {
	// The half-close exists so `cat file | astro exec -- cat` and
	// `astro exec -- psql < script` see EOF and finish, while the session
	// stays up for the output and exit frame that follow. Sending a full
	// close here would cut exactly that output, which is the regression
	// this test exists to catch.
	var frames []string
	deliveredAfterEOF := ""
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		for {
			f := s.recv()
			frames = append(frames, f.Type)
			if f.Type == "stdin_eof" {
				break
			}
			if f.Type == "close" {
				t.Error("EOF on stdin sent a close frame, which cuts pending output")
				return
			}
		}
		s.send(execFrame{Type: "stdout", Data: "the row you asked for\n"})
		s.send(execFrame{Type: "exit", Code: 0})
	})

	withExecSession(t, strings.NewReader("q\n"), notATerm{}, func(int) {})
	cmd, out, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"psql"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	deliveredAfterEOF = out.String()
	if !strings.Contains(strings.Join(frames, ","), "stdin_eof") {
		t.Errorf("frames after open = %v, want a stdin_eof", frames)
	}
	if deliveredAfterEOF != "the row you asked for\n" {
		t.Errorf("output after the half-close = %q, want it delivered intact", deliveredAfterEOF)
	}
}

func TestExecRoutesStdoutAndStderrToSeparateStreams(t *testing.T) {
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.send(execFrame{Type: "stdout", Data: "to stdout"})
		s.send(execFrame{Type: "stderr", Data: "to stderr"})
		s.send(execFrame{Type: "exit", Code: 0})
	})

	withExecSession(t, newBlockingReader(t), notATerm{}, func(int) {})
	cmd, out, errOut := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"sh"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if out.String() != "to stdout" {
		t.Errorf("stdout = %q", out.String())
	}
	if errOut.String() != "to stderr" {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestExecPropagatesRemoteExitCode(t *testing.T) {
	// Forwarding the remote's status rather than collapsing to 1 is what
	// lets a script branch on it. os.Exit is the production path, so the
	// seam is the only way to observe this without killing the test binary.
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.send(execFrame{Type: "stdout", Data: "boom\n"})
		s.send(execFrame{Type: "exit", Code: 42})
	})

	forwarded := -1
	withExecSession(t, newBlockingReader(t), notATerm{}, func(code int) { forwarded = code })
	cmd, out, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"false"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if forwarded != 42 {
		t.Errorf("exit status forwarded = %d, want 42", forwarded)
	}
	if out.String() != "boom\n" {
		t.Errorf("output before exit = %q, want it streamed before the status", out.String())
	}
}

func TestExecSuccessDoesNotForwardAnExitStatus(t *testing.T) {
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.send(execFrame{Type: "exit", Code: 0})
	})

	called := false
	withExecSession(t, newBlockingReader(t), notATerm{}, func(int) { called = true })
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"true"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if called {
		t.Error("exit 0 forwarded a status; it must return normally so deferred cleanup runs")
	}
}

func TestExecErrorFrameGoesToStderrWithoutEndingTheSession(t *testing.T) {
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.send(execFrame{Type: "error", Message: "container restarting"})
		s.send(execFrame{Type: "stdout", Data: "still here\n"})
		s.send(execFrame{Type: "exit", Code: 0})
	})

	withExecSession(t, newBlockingReader(t), notATerm{}, func(int) {})
	cmd, out, errOut := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"sh"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if !strings.Contains(errOut.String(), "container restarting") {
		t.Errorf("stderr = %q, want the relay's message", errOut.String())
	}
	if out.String() != "still here\n" {
		t.Errorf("an error frame ended the session; stdout = %q", out.String())
	}
}

func TestExecEntersRawModeAndRestoresIt(t *testing.T) {
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.send(execFrame{Type: "exit", Code: 0})
	})

	tty := &fakeTerm{cols: 80, rows: 24}
	withExecSession(t, newBlockingReader(t), tty, func(int) {})
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"sh"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	entered, restored := tty.counts()
	if entered != 1 {
		t.Errorf("raw mode entered %d times, want 1", entered)
	}
	// Left un-restored, the operator's shell keeps no echo and no line
	// editing after exec returns -- the failure people describe as "my
	// terminal is broken".
	if restored == 0 {
		t.Error("raw mode was never restored")
	}
}

func TestExecWarnsButContinuesWhenRawModeFails(t *testing.T) {
	// Documented degradation: without raw mode the terminal keeps ISIG, so
	// Ctrl-C interrupts the remote instead of being passed through as 0x03.
	// exec.go says that out loud rather than swallowing it (#72).
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.send(execFrame{Type: "stdout", Data: "session ran anyway\n"})
		s.send(execFrame{Type: "exit", Code: 0})
	})

	tty := &fakeTerm{cols: 80, rows: 24, makeRawErr: errors.New("inappropriate ioctl for device")}
	withExecSession(t, newBlockingReader(t), tty, func(int) {})
	cmd, out, errOut := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"sh"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if !strings.Contains(errOut.String(), "raw mode") {
		t.Errorf("stderr = %q, want a warning naming raw mode", errOut.String())
	}
	if out.String() != "session ran anyway\n" {
		t.Error("a raw-mode failure aborted the session instead of degrading")
	}
	if _, restored := tty.counts(); restored != 0 {
		t.Error("restored a terminal state that was never established")
	}
}

func TestExecRequiresAnApp(t *testing.T) {
	withExecSession(t, newBlockingReader(t), notATerm{}, func(int) {})
	execApp = ""
	cmd, _, _ := execTestCmd()

	err := runExec(cmd, context.Background(), api.NewClient("http://127.0.0.1:1", "tok", false), []string{"sh"})
	if err == nil || !strings.Contains(err.Error(), "--app is required") {
		t.Fatalf("err = %v, want the --app requirement", err)
	}
}

var _ io.Reader = (*blockingReader)(nil)
