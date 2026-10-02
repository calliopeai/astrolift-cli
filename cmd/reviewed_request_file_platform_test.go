package cmd

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestPrivateFileReviewedRequestRecoveryPreservesScopeAndKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reviewed.json")
	request := reviewedStartRequest{
		Format: 1, Kind: "pipeline", Server: "https://api.example.test", ActorUserID: 42,
		OrganizationID: uuid.NewString(), TargetID: uuid.NewString(), TargetName: "reviewed",
		RequestID: uuid.NewString(), Version: 7, Ref: "main",
	}
	if err := createReviewedRequest(path, request); err != nil {
		t.Fatal(err)
	}
	stored, err := readReviewedRequest(path)
	if err != nil || !reflect.DeepEqual(request, *stored) {
		t.Fatalf("reviewed request failed private-file reload: %v", err)
	}
	if err := stored.checkScope(request.Kind, request.Server, request.OrganizationID, request.ActorUserID); err != nil {
		t.Fatal(err)
	}
	if err := stored.checkScope(request.Kind, request.Server, request.OrganizationID, 43); err == nil {
		t.Fatal("private-file reload discarded the actor boundary")
	}
	changed := request
	changed.RequestID = uuid.NewString()
	if err := createReviewedRequest(path, changed); err == nil {
		t.Fatal("private-file recovery replaced the original request")
	}
	again, err := readReviewedRequest(path)
	if err != nil || again.RequestID != request.RequestID {
		t.Fatal("original recovery identity changed")
	}
}
