package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// reviewedStartRequest is metadata only. Inputs and bearer credentials never
// belong in this recovery record; the server owns the encrypted frozen inputs.
type reviewedStartRequest struct {
	Format            int    `json:"format"`
	Kind              string `json:"kind"`
	Server            string `json:"server"`
	OrganizationID    string `json:"organizationId"`
	ActorUserID       int    `json:"actorUserId"`
	TargetID          string `json:"targetId"`
	TargetName        string `json:"targetName"`
	RequestID         string `json:"requestId"`
	Version           int    `json:"version,omitempty"`
	Ref               string `json:"ref,omitempty"`
	Revision          string `json:"revision,omitempty"`
	InputSchemaDigest string `json:"inputSchemaDigest,omitempty"`
}

func reviewedRequestScope(cmd *cobra.Command, client *api.Client) (string, int, error) {
	if client.Org() == "" {
		return "", 0, errors.New("select an organization before reviewing a start")
	}
	u, err := url.Parse(client.BaseURL())
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", 0, errors.New("the API URL must identify a server without embedded credentials, query or fragment")
	}
	identity, err := fetchPermissionIdentity(cmd, client)
	if err != nil {
		return "", 0, err
	}
	return client.BaseURL(), identity.UserID, nil
}

func (r reviewedStartRequest) checkScope(kind, server, org string, actor int) error {
	if r.Format != 1 || r.Kind != kind || r.Server != server || r.OrganizationID != org || r.ActorUserID != actor {
		return errors.New("request file belongs to another server, organization, actor or command; use its original scope to reconcile")
	}
	if _, err := uuid.Parse(r.RequestID); err != nil {
		return errors.New("request file has an invalid request ID")
	}
	if _, err := uuid.Parse(r.TargetID); err != nil {
		return errors.New("request file has an invalid target GUID")
	}
	return nil
}

func readReviewedRequest(filename string) (*reviewedStartRequest, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16384 {
		return nil, errors.New("request file must be a regular file with mode 0600 and at most 16 KiB")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 || opened.Size() > 16384 {
		return nil, errors.New("request file changed while opening")
	}
	var r reviewedStartRequest
	d := json.NewDecoder(io.LimitReader(f, 16385))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid request file: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("request file contains extra data")
	}
	return &r, nil
}

// Exclusive creation prevents a second process from replacing a persisted key.
// Keep a partial record on I/O failure: dispatch has not happened, and silently
// replacing it would erase the evidence needed to investigate an uncertain run.
func createReviewedRequest(filename string, r reviewedStartRequest) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("saving request identity before dispatch: %w", err)
	}
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(filename))
		if err != nil {
			return err
		}
		err = dir.Sync()
		closeErr = dir.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
