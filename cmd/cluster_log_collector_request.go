package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/google/uuid"
)

const collectorRequestKind = "cluster-log-collector"

// The original source and retention belong to the durable reviewed tuple;
// reader credentials, chart values and log bodies do not belong in this file.
type collectorInstallRequest struct {
	reviewedStartRequest
	RetentionDays int `json:"retentionDays"`
}

func (r collectorInstallRequest) validateTuple() error {
	if r.Format != 1 || r.Kind != collectorRequestKind || r.Server == "" ||
		r.OrganizationID == "" || r.ActorUserID < 1 || strings.TrimSpace(r.TargetName) == "" ||
		r.Version < 1 || strings.TrimSpace(r.ExpectedSource) == "" {
		return errors.New("collector request lacks its original reviewed tuple; preserve the file")
	}
	for _, value := range []string{r.TargetID, r.RequestID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return errors.New("collector request requires original canonical nonzero target and request UUIDs")
		}
	}
	if !collectorRetentionSupported(r.RetentionDays) {
		return errors.New("collector request lacks its original supported retention; preserve the file")
	}
	return nil
}

func (r collectorInstallRequest) checkRecovery(server, organization string, actor int, slug string, retention int, retentionChanged bool) error {
	if err := r.validateTuple(); err != nil {
		return err
	}
	if err := r.checkScope(collectorRequestKind, server, organization, actor); err != nil {
		return err
	}
	if r.TargetName != slug || retentionChanged && r.RetentionDays != retention {
		return errors.New("collector selector or retention differs from the original request; preserve the file")
	}
	return nil
}

func collectorRetentionSupported(days int) bool {
	switch days {
	case 1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1827, 3653:
		return true
	default:
		return false
	}
}

func readCollectorInstallRequest(filename string) (*collectorInstallRequest, error) {
	f, err := privatefile.Open(filename, 16384)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var r collectorInstallRequest
	decoder := json.NewDecoder(io.LimitReader(f, 16385))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&r) != nil {
		return nil, errors.New("invalid collector request file; preserve it for review")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("collector request file contains extra data; preserve it for review")
	}
	if err := r.validateTuple(); err != nil {
		return nil, err
	}
	return &r, nil
}

func createCollectorInstallRequest(filename string, r collectorInstallRequest) error {
	if err := r.validateTuple(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return errors.New("collector request could not be encoded")
	}
	f, err := privatefile.CreateExclusive(filename)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
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
	return privatefile.SyncParent(filename)
}
