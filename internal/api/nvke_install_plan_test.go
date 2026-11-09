package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNVKEInstallPlanPrivateTransportRefusesRedirectsAndPreservesClientTimeout(t *testing.T) {
	for _, kind := range []string{"prepare", "review", "retry", "items", "workloads", "charts-prepare", "charts-review", "charts-retry"} {
		t.Run(kind, func(t *testing.T) {
			forwarded := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded++
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = w.Write([]byte(`{"data":{}}`))
			}))
			defer destination.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
			}))
			defer source.Close()
			c := NewClient(source.URL, "PRIVATE_TOKEN", false)
			selected := c.nvkeInstallPlanClient()
			if selected.httpClient.Timeout != 360*time.Second || c.httpClient.Timeout != 30*time.Second {
				t.Fatal("plan timeout changed the shared API client")
			}
			var err error
			switch kind {
			case "prepare":
				_, err = c.PrepareNVKEInstallPlan(context.Background(), NVKEInstallPlanPrepare{RequestText: "PRIVATE_REQUEST"})
			case "review", "retry":
				_, err = c.ReviewNVKEInstallPlan(context.Background(), NVKEInstallPlanReview{}, kind == "retry")
			case "items":
				_, err = c.NVKEInstallPlanItems(context.Background(), NVKEInstallPlanReview{}, "digest", nil, 25, "")
			case "workloads":
				_, err = c.NVKEInstallPlanWorkloads(context.Background(), NVKEInstallPlanReview{}, "digest", nil, 25, "")
			case "charts-prepare":
				_, err = c.PrepareNVKEInstallPlanCharts(context.Background(), NVKEInstallPlanChartsPrepare{RequestText: "PRIVATE_REQUEST"})
			case "charts-review", "charts-retry":
				_, err = c.ReviewNVKEInstallPlanCharts(context.Background(), NVKEInstallPlanChartsReview{}, kind == "charts-retry")
			}
			if err == nil || forwarded != 0 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe plan transport kind=%s forwarded=%d err=%v", kind, forwarded, err)
			}
		})
	}
}
