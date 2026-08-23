package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// The session-level harness #78 asked for.
//
// cmd/exec_test.go covered execWSURL and nothing else, so every behaviour
// that carries a stated rationale in exec.go -- the stdin half-close, exit
// status forwarding, raw-mode degradation, resize, interrupt forwarding --
// shipped with no test that would notice it breaking. #72 was reported from
// production for exactly that reason.
//
// This file stands up the server half of the frame protocol documented at
// the top of exec.go and drives runExec against it.

// execFrame is both directions of the protocol in one struct. The two
// halves share no field, so decoding either into this is unambiguous.
type execFrame struct {
	Type      string   `json:"type"`
	Data      string   `json:"data"`
	Code      int      `json:"code"`
	Message   string   `json:"message"`
	Command   []string `json:"command,omitempty"`
	Container string   `json:"container,omitempty"`
	TTY       bool     `json:"tty,omitempty"`
	Rows      int      `json:"rows,omitempty"`
	Cols      int      `json:"cols,omitempty"`
}

// relaySession is the scripting surface a test gets: read what the client
// sent, send what the container would have.
type relaySession struct {
	t    *testing.T
	conn *websocket.Conn
	in   <-chan execFrame
}

// recv returns the next frame the client sent, failing the test rather than
// blocking forever if it never arrives. Every wait in this file goes through
// here so a hang surfaces as a named failure instead of a package timeout.
func (s *relaySession) recv() execFrame {
	s.t.Helper()
	select {
	case f, ok := <-s.in:
		if !ok {
			s.t.Fatal("client closed the socket while a frame was expected")
		}
		return f
	case <-time.After(5 * time.Second):
		s.t.Fatal("timed out waiting for a frame from the client")
		return execFrame{}
	}
}

// recvUntil skips frames until one of the given type arrives. Used where the
// interesting frame is preceded by traffic a test does not care about.
func (s *relaySession) recvUntil(kind string) execFrame {
	s.t.Helper()
	for i := 0; i < 20; i++ {
		if f := s.recv(); f.Type == kind {
			return f
		}
	}
	s.t.Fatalf("no %q frame in the first 20 frames", kind)
	return execFrame{}
}

func (s *relaySession) send(f execFrame) {
	s.t.Helper()
	if err := s.conn.WriteJSON(f); err != nil {
		s.t.Errorf("relay write %s: %v", f.Type, err)
	}
}

// fakeRelay is the server half of a session. wait() is what makes anything
// the script captured safe to read: the script runs on the httptest server's
// goroutine, so without an explicit edge every assertion on a captured frame
// is a data race, benign-looking right up until it is not.
type fakeRelay struct {
	URL  string
	done chan struct{}
}

func (r *fakeRelay) wait(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("relay script did not finish")
	}
}

// newFakeRelay serves the exec WebSocket at any path and runs script once
// the socket is up. The script's return closes the socket, which is what
// ends runExec's read loop.
func newFakeRelay(t *testing.T, script func(s *relaySession)) *fakeRelay {
	t.Helper()
	up := websocket.Upgrader{}
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			close(done)
			return
		}
		defer conn.Close()
		in := make(chan execFrame, 64)
		go func() {
			defer close(in)
			for {
				_, data, rerr := conn.ReadMessage()
				if rerr != nil {
					return
				}
				var f execFrame
				if json.Unmarshal(data, &f) == nil {
					in <- f
				}
			}
		}()
		script(&relaySession{t: t, conn: conn, in: in})
		close(done)
	}))
	t.Cleanup(srv.Close)
	return &fakeRelay{URL: srv.URL, done: done}
}

// fakeTerm reports a TTY of a fixed size. makeRawErr forces the raw-mode
// failure path, which exec.go degrades through rather than aborting.
type fakeTerm struct {
	mu         sync.Mutex
	cols, rows int
	makeRawErr error
	rawEntered int
	restored   int
}

func (f *fakeTerm) IsTerminal(int) bool { return true }

func (f *fakeTerm) GetSize(int) (int, int, error) { return f.cols, f.rows, nil }

func (f *fakeTerm) MakeRaw(int) (*term.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.makeRawErr != nil {
		return nil, f.makeRawErr
	}
	f.rawEntered++
	return &term.State{}, nil
}

func (f *fakeTerm) Restore(int, *term.State) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restored++
	return nil
}

func (f *fakeTerm) counts() (entered, restored int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rawEntered, f.restored
}

// notATerm is the non-interactive default: a piped run.
type notATerm struct{}

func (notATerm) IsTerminal(int) bool              { return false }
func (notATerm) GetSize(int) (int, int, error)    { return 0, 0, io.EOF }
func (notATerm) MakeRaw(int) (*term.State, error) { return nil, io.EOF }
func (notATerm) Restore(int, *term.State) error   { return nil }

// blockingReader never yields and never EOFs, for sessions whose stdin is
// irrelevant. A plain bytes.Reader would EOF immediately and send a
// stdin_eof frame into tests that are not expecting one.
type blockingReader struct{ release chan struct{} }

func newBlockingReader(t *testing.T) *blockingReader {
	b := &blockingReader{release: make(chan struct{})}
	t.Cleanup(func() { close(b.release) })
	return b
}

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.release
	return 0, io.EOF
}

// execTestCmd captures stdout and stderr separately; agentTestCmd only
// captures stdout, and the raw-mode warning goes to stderr.
func execTestCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	c := &cobra.Command{}
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	c.SetOut(out)
	c.SetErr(errOut)
	return c, out, errOut
}

// withExecSession installs the seams for one session and restores them
// after, so tests do not leak process-global state into each other.
func withExecSession(t *testing.T, stdin io.Reader, tty execTerminal, exit func(int)) {
	t.Helper()
	prevStdin, prevTerm, prevExit := execStdin, execTerm, execExit
	prevPod, prevApp, prevContainer, prevNoTTY := execPod, execApp, execContainer, execNoTTY
	execStdin, execTerm, execExit = stdin, tty, exit
	execApp, execPod, execContainer, execNoTTY = "web", "web-abc", "app", false
	t.Cleanup(func() {
		execStdin, execTerm, execExit = prevStdin, prevTerm, prevExit
		execPod, execApp, execContainer, execNoTTY = prevPod, prevApp, prevContainer, prevNoTTY
	})
}
