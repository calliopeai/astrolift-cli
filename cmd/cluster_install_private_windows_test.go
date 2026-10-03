//go:build windows

package cmd

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

// This changes the actual DACL, not Go's POSIX-looking mode bits on Windows.
func broadenInstallRequestACL(t *testing.T, filename string) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(filename, windows.SE_FILE_OBJECT, flags, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(sd)
}

func checkInstallConsumerRefusesBroadWindowsACL(t *testing.T, collector bool) {
	t.Helper()
	c, out, file := collectorCommand(t)
	mutations := 0
	srv := agentInstallServer(t, func(q gqlRequest, w http.ResponseWriter) {
		if strings.Contains(q.Query, "mutation") {
			mutations++
		}
		writePipelineTestResponse(t, w, collectorHTTPData())
	})
	defer srv.Close()
	if collector {
		r := collectorRequestFixture()
		r.Server = srv.URL
		if err := createCollectorInstallRequest(file, r); err != nil {
			t.Fatal(err)
		}
	} else {
		r := reviewedStartRequest{Format: 1, Kind: clusterAgentInstallKind, Server: srv.URL, OrganizationID: reviewedPipelineTestOrg, ActorUserID: 42, TargetID: reviewedPipelineTestID, TargetName: "production", Version: 7, RequestID: uuid.NewString(), ExpectedSource: agentInstallTestSource}
		if err := createReviewedRequest(file, r); err != nil {
			t.Fatal(err)
		}
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	broadenInstallRequestACL(t, file)
	client := api.NewClient(srv.URL, "PRIVATE_BEARER_MARKER", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if collector {
		err = runClusterInstallLogCollector(c, context.Background(), client, "production", 30)
	} else {
		err = runClusterInstallAgent(c, context.Background(), client, "production", "", 0)
	}
	if err == nil || !strings.Contains(err.Error(), "Windows ACL") || strings.Contains(err.Error(), "PRIVATE_BEARER_MARKER") || mutations != 0 || out.Len() != 0 {
		t.Fatalf("actual permissive ACL was not refused before dispatch: %v; mutations=%d", err, mutations)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("refusal rewrote the original request")
	}
}

func TestClusterAgentInstallWindowsBroadACLRefusedBeforeDispatch(t *testing.T) {
	checkInstallConsumerRefusesBroadWindowsACL(t, false)
}
func TestCollectorWindowsBroadACLRefusedBeforeDispatch(t *testing.T) {
	checkInstallConsumerRefusesBroadWindowsACL(t, true)
}
