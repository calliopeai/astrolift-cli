package cmd

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ---- fixtures ---------------------------------------------------------------

func domainRow(d appDomain) map[string]interface{} {
	raw, err := json.Marshal(d)
	if err != nil {
		panic(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	return m
}

func domainsListData(rows []appDomain) map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(rows))
	for _, d := range rows {
		items = append(items, domainRow(d))
	}
	return map[string]interface{}{"astroliftAppDomains": items}
}

// ---- list --------------------------------------------------------------------

func TestAppDomainsListSendsAppSlug(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, domainsListData(nil), &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppDomainsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppDomainsList: %v", err)
	}
	if captured.Variables["appSlug"] != "web" {
		t.Errorf("appSlug = %v, want web", captured.Variables["appSlug"])
	}
}

// cert_state (DNS validation) and certificate_state (TLS issuance) are
// different axes on the wire -- a domain can be DNS-validated with a failed
// certificate, so both must render distinctly.
func TestAppDomainsListRendersTableWithBothCertAxes(t *testing.T) {
	rows := []appDomain{{
		Hostname: "app.example.com", IsActive: true, ValidationMethod: "dns_txt",
		CertState: "validated", CertificateState: "failed", CertExpiresAt: nil,
	}}
	srv := gqlServer(t, domainsListData(rows), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppDomainsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppDomainsList: %v", err)
	}
	got := out.String()
	for _, want := range []string{"app.example.com", "yes", "dns_txt", "validated", "failed", "1 domain(s) shown."} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestAppDomainsListEmptyNamesTheApp(t *testing.T) {
	srv := gqlServer(t, domainsListData(nil), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppDomainsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppDomainsList: %v", err)
	}
	if !strings.Contains(out.String(), `"web"`) {
		t.Errorf("empty message should name the app: %s", out.String())
	}
}

func TestAppDomainsListSurfacesValidationAndCertificateErrorsOnStderr(t *testing.T) {
	rows := []appDomain{{
		Hostname: "broken.example.com", LastValidationError: "TXT record not found",
		LastCertificateError: "ACM request throttled",
	}}
	srv := gqlServer(t, domainsListData(rows), nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	errBuf := &strings.Builder{}
	cmd.SetErr(errBuf)
	if err := runAppDomainsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppDomainsList: %v", err)
	}
	got := errBuf.String()
	if !strings.Contains(got, "broken.example.com: validation error: TXT record not found") {
		t.Errorf("missing validation error note: %s", got)
	}
	if !strings.Contains(got, "broken.example.com: certificate error: ACM request throttled") {
		t.Errorf("missing certificate error note: %s", got)
	}
}

func TestAppDomainsListJSONIncludesRequiredDNSRecords(t *testing.T) {
	rows := []appDomain{{
		Hostname: "app.example.com",
		RequiredDNSRecords: []appDomainRequiredRecord{
			{Kind: "TXT", Name: "_astrolift-challenge.app.example.com", Value: "abc123", TTL: 300, Propagated: false, Message: "not yet observed"},
		},
	}}
	srv := gqlServer(t, domainsListData(rows), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppDomainsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppDomainsList --json: %v", err)
	}
	var decoded []appDomain
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding json output: %v", err)
	}
	if len(decoded) != 1 || len(decoded[0].RequiredDNSRecords) != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded[0].RequiredDNSRecords[0].Name != "_astrolift-challenge.app.example.com" {
		t.Errorf("required dns record = %+v", decoded[0].RequiredDNSRecords[0])
	}
}

func TestAppDomainsListSurfacesGraphQLError(t *testing.T) {
	srv := graphQLErrorServer(t, "permission denied: app.read")
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppDomainsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v, want the server's message surfaced", err)
	}
}

// ---- pause / resume ------------------------------------------------------------

func pauseServer(t *testing.T, rows []appDomain, result map[string]interface{}, mut *gqlRequest) *httptest.Server {
	t.Helper()
	return gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "setCustomDomainPaused") {
			if mut != nil {
				*mut = req
			}
			return map[string]interface{}{"setCustomDomainPaused": result}
		}
		return domainsListData(rows)
	})
}

func pauseResult(hostname string, paused bool) map[string]interface{} {
	return map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{"id": "d1", "hostname": hostname, "isPaused": paused},
	}
}

var pauseRows = []appDomain{
	{ID: "d1", Hostname: "app.example.com"},
	{ID: "d2", Hostname: "www.example.com"},
}

func TestAppDomainsPauseResolvesHostnameAndSendsInput(t *testing.T) {
	var mut gqlRequest
	srv := pauseServer(t, pauseRows, pauseResult("app.example.com", true), &mut)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppDomainSetPaused(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "APP.Example.com", true); err != nil {
		t.Fatalf("pause: %v", err)
	}
	input, _ := mut.Variables["input"].(map[string]interface{})
	if input["id"] != "d1" || input["paused"] != true {
		t.Errorf("input = %v, want id d1 paused true", input)
	}
	for _, want := range []string{"app.example.com is now paused", "503", "resume"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q: %s", want, out.String())
		}
	}
}

func TestAppDomainsResumeSendsPausedFalse(t *testing.T) {
	var mut gqlRequest
	srv := pauseServer(t, pauseRows, pauseResult("www.example.com", false), &mut)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppDomainSetPaused(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "www.example.com", false); err != nil {
		t.Fatalf("resume: %v", err)
	}
	input, _ := mut.Variables["input"].(map[string]interface{})
	if input["id"] != "d2" || input["paused"] != false {
		t.Errorf("input = %v, want id d2 paused false", input)
	}
	if !strings.Contains(out.String(), "www.example.com is now resumed") {
		t.Errorf("output = %s", out.String())
	}
}

func TestAppDomainsPauseUnknownHostnameListsHostnames(t *testing.T) {
	var mut gqlRequest
	srv := pauseServer(t, pauseRows, pauseResult("x", true), &mut)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppDomainSetPaused(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "nope.example.com", true)
	if err == nil || !strings.Contains(err.Error(), "app.example.com, www.example.com") {
		t.Fatalf("err = %v, want the app's hostnames listed", err)
	}
	if mut.Query != "" {
		t.Error("mutation must not be sent for an unknown hostname")
	}
}

func TestAppDomainsPauseSurfacesMutationError(t *testing.T) {
	res := map[string]interface{}{
		"ok": false, "data": nil,
		"errors": []map[string]interface{}{{"code": "forbidden", "message": "permission denied: app.write"}},
	}
	srv := pauseServer(t, pauseRows, res, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppDomainSetPaused(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "app.example.com", true)
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v, want the server's message surfaced", err)
	}
}

func TestAppDomainsPauseSurfacesGraphQLError(t *testing.T) {
	srv := graphQLErrorServer(t, "boom")
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppDomainSetPaused(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "app.example.com", true)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestAppDomainsPauseJSON(t *testing.T) {
	srv := pauseServer(t, pauseRows, pauseResult("app.example.com", true), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppDomainSetPaused(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "app.example.com", true); err != nil {
		t.Fatalf("pause --json: %v", err)
	}
	var decoded appDomainPauseState
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding json output: %v", err)
	}
	if decoded.Hostname != "app.example.com" || !decoded.IsPaused {
		t.Errorf("decoded = %+v", decoded)
	}
}
