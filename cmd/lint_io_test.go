package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

type failedConfirmationReader struct{ err error }

func (r failedConfirmationReader) Read([]byte) (int, error) { return 0, r.err }

func TestDestructiveConfirmationReadFailureSendsNoRequest(t *testing.T) {
	priorCancel, priorRemove := agentCancelYes, boxRmYes
	agentCancelYes, boxRmYes = false, false
	defer func() { agentCancelYes, boxRmYes = priorCancel, priorRemove }()
	scanErr := errors.New("input device failed")
	for _, operation := range []struct {
		name string
		run  func(*cobra.Command, *api.Client) error
	}{
		{"cancel", func(cmd *cobra.Command, client *api.Client) error {
			return runAgentCancel(cmd, context.Background(), client, &config.Config{}, "task-1")
		}},
		{"remove", func(cmd *cobra.Command, client *api.Client) error {
			return runBoxRm(cmd, context.Background(), client, "box-1")
		}},
	} {
		for _, input := range []struct {
			name   string
			reader io.Reader
			want   error
		}{
			{"EOF", strings.NewReader(""), io.EOF},
			{"read failure", failedConfirmationReader{scanErr}, scanErr},
		} {
			t.Run(operation.name+"/"+input.name, func(t *testing.T) {
				requests := 0
				server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
				defer server.Close()
				cmd, _ := agentTestCmd()
				cmd.SetIn(input.reader)
				err := operation.run(cmd, api.NewClient(server.URL, "token", false))
				if !errors.Is(err, input.want) {
					t.Fatalf("error=%v, want %v", err, input.want)
				}
				if requests != 0 {
					t.Fatalf("sent %d destructive requests after failed confirmation", requests)
				}
			})
		}
	}
}

type extractedBinaryWriter struct {
	buffer             bytes.Buffer
	writeErr, closeErr error
	closed             bool
}

func (w *extractedBinaryWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.buffer.Write(p)
}
func (w *extractedBinaryWriter) Close() error { w.closed = true; return w.closeErr }

func TestExtractedBinaryRequiresSuccessfulClose(t *testing.T) {
	copyErr, closeErr := errors.New("disk write failed"), errors.New("disk close failed")
	for _, tc := range []struct {
		name                     string
		writeErr, closeErr, want error
	}{
		{"complete", nil, nil, nil},
		{"close failure", nil, closeErr, closeErr},
		{"copy failure", copyErr, nil, copyErr},
		{"both fail", copyErr, closeErr, copyErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := &extractedBinaryWriter{writeErr: tc.writeErr, closeErr: tc.closeErr}
			err := writeExtractedBinary(out, strings.NewReader("binary bytes"))
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			if !out.closed {
				t.Fatal("extracted output was not closed")
			}
			if tc.writeErr == nil && out.buffer.String() != "binary bytes" {
				t.Fatal("binary contents changed")
			}
		})
	}
}
