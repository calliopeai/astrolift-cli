// Package cmd — `astro dev dispatch-stub` subcommand.
//
// Starts a minimal in-process stub Dispatch Service for local development.
// The stub:
//  1. Registers itself with the Controller on startup.
//  2. Sends a heartbeat loop to keep the registration alive.
//  3. Polls for QUEUED tasks and advances them through the state machine
//     on a configurable delay (simulating PROVISIONING → RUNNING → COMPLETED).
//  4. Does NOT spawn real containers — task execution is fully simulated.
//  5. Serves fake log lines on the log streaming endpoint so that
//     `astro agent logs --follow` works end-to-end.
//  6. Optionally simulates failures at a configurable rate.
//
// This gives a developer a one-command local stack that exercises the full
// Controller ↔ Dispatch Service protocol without requiring a K8s cluster
// or ECS environment.
//
// Issue: calliopeai/astrolift#63
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ---- top-level `astro dev` group -------------------------------------------

var devCmd = &cobra.Command{
	Use:   "dev",
	Short: "Local development utilities",
	Long:  `Commands for local development and testing without a real cluster.`,
}

// ---- flags -----------------------------------------------------------------

var (
	stubPort        int
	stubName        string
	stubDelay       time.Duration
	stubFailureRate float64
	stubAPIURL      string
	stubToken       string
)

// ---- astro dev dispatch-stub start -----------------------------------------

var devDispatchStubCmd = &cobra.Command{
	Use:   "dispatch-stub",
	Short: "Run a stub Dispatch Service for local development",
}

var devDispatchStubStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the stub Dispatch Service",
	Long: `Starts a minimal stub Dispatch Service that registers with the
Controller and simulates the full PROVISIONING → RUNNING → COMPLETED
task lifecycle without spawning real containers.

Useful for testing astrolift_agent_dispatch features (models, GraphQL,
workflow patterns, UI) without a K8s cluster or ECS environment.

Environment variables:
  ASTROLIFT_API_URL     — Controller base URL (overrides --api-url)
  ASTROLIFT_DEPLOY_TOKEN — Auth token (overrides --token)
  STUB_FAILURE_RATE     — Float 0–1; fraction of tasks that fail (overrides --failure-rate)

Example:
  astro dev dispatch-stub start --name local-stub --delay 2s`,
	RunE: runDispatchStub,
}

// ---- stub state ------------------------------------------------------------

// stubDispatcher holds the runtime state of the running stub.
type stubDispatcher struct {
	apiURL      string
	token       string
	name        string
	delay       time.Duration
	failureRate float64
	httpClient  *http.Client

	// dispatcherID is set after successful registration
	dispatcherID string

	// mu guards activeTasks
	mu          sync.Mutex
	activeTasks map[string]*stubTask
}

type stubTask struct {
	ID     string
	Status string
	Logs   []string
}

// ---- registration / heartbeat ----------------------------------------------

func (s *stubDispatcher) register(ctx context.Context) error {
	body := map[string]interface{}{
		"name":     s.name,
		"backend":  "stub",
		"endpoint": fmt.Sprintf("http://localhost:%d", stubPort),
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling registration body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx,
		http.MethodPost,
		s.apiURL+"/api/dispatch/v1/register/",
		bytes.NewReader(encoded),
	)
	if err != nil {
		return fmt.Errorf("building registration request: %w", err)
	}
	s.setHeaders(req)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("registration request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("registration rejected with HTTP %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		// Non-fatal: some Controller builds return empty body on 200
		return nil
	}
	s.dispatcherID = result.ID
	return nil
}

func (s *stubDispatcher) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx,
				http.MethodPost,
				s.apiURL+"/api/dispatch/v1/heartbeat/",
				nil,
			)
			if err != nil {
				continue
			}
			s.setHeaders(req)
			resp, err := s.httpClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	}
}

// ---- task poll / advance ---------------------------------------------------

func (s *stubDispatcher) poll(ctx context.Context, out *os.File) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.fetchAndAdvance(ctx, out)
		}
	}
}

func (s *stubDispatcher) fetchAndAdvance(ctx context.Context, out *os.File) {
	req, err := http.NewRequestWithContext(ctx,
		http.MethodGet,
		s.apiURL+"/api/dispatch/v1/tasks/?status=queued&limit=10",
		nil,
	)
	if err != nil {
		return
	}
	s.setHeaders(req)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return
	}

	var result struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}

	for _, t := range result.Results {
		s.mu.Lock()
		_, already := s.activeTasks[t.ID]
		if !already {
			s.activeTasks[t.ID] = &stubTask{
				ID:     t.ID,
				Status: "queued",
			}
		}
		s.mu.Unlock()

		if !already {
			go s.advanceTask(ctx, t.ID, out)
		}
	}
}

// advanceTask simulates the full task lifecycle in a goroutine.
func (s *stubDispatcher) advanceTask(ctx context.Context, taskID string, out *os.File) {
	shouldFail := rand.Float64() < s.failureRate

	transitions := []string{"provisioning", "running"}
	if shouldFail {
		transitions = append(transitions, "failed")
	} else {
		transitions = append(transitions, "completed")
	}

	for _, status := range transitions {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.delay):
		}

		s.mu.Lock()
		if task, ok := s.activeTasks[taskID]; ok {
			task.Status = status
			logLine := fmt.Sprintf("[stub] task %s → %s", taskID[:min(8, len(taskID))], status)
			task.Logs = append(task.Logs, logLine)
		}
		s.mu.Unlock()

		fmt.Fprintf(out, "  task %-12s → %s\n", taskID[:min(12, len(taskID))], status)

		errMsg := ""
		if shouldFail && status == "failed" {
			errMsg = "stub: simulated failure (STUB_FAILURE_RATE)"
		}
		if err := s.reportStatus(ctx, taskID, status, errMsg); err != nil {
			fmt.Fprintf(out, "  warning: status report failed for %s: %v\n", taskID, err)
		}
	}

	s.mu.Lock()
	delete(s.activeTasks, taskID)
	s.mu.Unlock()
}

// reportStatus PATCHes the task status back to the Controller.
func (s *stubDispatcher) reportStatus(ctx context.Context, taskID, status, errMsg string) error {
	body := map[string]interface{}{
		"status": status,
	}
	if errMsg != "" {
		body["error_message"] = errMsg
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx,
		http.MethodPatch,
		fmt.Sprintf("%s/api/dispatch/v1/tasks/%s/", s.apiURL, taskID),
		bytes.NewReader(encoded),
	)
	if err != nil {
		return err
	}
	s.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ---- HTTP server (log streaming) -------------------------------------------

// serveHTTP starts the stub's own HTTP server so that the Controller can
// proxy log-stream requests to us. The only endpoint needed is:
//
//	GET /dispatch/tasks/<id>/logs/stream
func (s *stubDispatcher) serveHTTP(ctx context.Context, out *os.File) {
	mux := http.NewServeMux()

	mux.HandleFunc("/dispatch/tasks/", func(w http.ResponseWriter, r *http.Request) {
		// Expect: /dispatch/tasks/<id>/logs/stream
		parts := splitPath(r.URL.Path)
		if len(parts) < 4 || parts[3] != "stream" {
			http.NotFound(w, r)
			return
		}
		taskID := parts[2]

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		sent := 0
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				task, exists := s.activeTasks[taskID]
				var logs []string
				var status string
				if exists {
					logs = task.Logs[sent:]
					sent += len(logs)
					status = task.Status
				}
				s.mu.Unlock()

				for _, line := range logs {
					fmt.Fprintf(w, "data: %s\n\n", line)
					flusher.Flush()
				}

				terminal := status == "completed" || status == "failed" ||
					status == "cancelled" || status == "timed_out"
				if !exists || terminal {
					fmt.Fprintf(w, "event: done\ndata: %s\n\n", status)
					flusher.Flush()
					return
				}
			}
		}
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", stubPort),
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	fmt.Fprintf(out, "Stub HTTP server listening on :%d\n", stubPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(out, "Stub HTTP server error: %v\n", err)
	}
}

// ---- main RunE -------------------------------------------------------------

func runDispatchStub(cmd *cobra.Command, _ []string) error {
	// Resolve config from flags → env → defaults
	apiURL := stubAPIURL
	if v := os.Getenv("ASTROLIFT_API_URL"); v != "" {
		apiURL = v
	}
	pinnedServer := ""
	if strings.TrimSpace(viper.GetString("server")) != "" {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		slug, entry, err := selectedServer(cfg, nil)
		if err != nil {
			return err
		}
		if apiURL != "" && strings.TrimRight(apiURL, "/") != strings.TrimRight(entry.APIURL, "/") {
			return fmt.Errorf("API URL override does not match registered server %q", slug)
		}
		pinnedServer, apiURL = slug, entry.APIURL
	}
	if apiURL == "" {
		// Fall back to active server config
		if cfg, err := config.Load(); err == nil && cfg.CurrentServer != "" {
			if entry, ok := cfg.Servers[cfg.CurrentServer]; ok {
				apiURL = entry.APIURL
			}
		}
	}
	if apiURL == "" {
		return fmt.Errorf("--api-url or ASTROLIFT_API_URL required (or run `astro server add`)")
	}

	token := stubToken
	if v := os.Getenv("ASTROLIFT_DEPLOY_TOKEN"); v != "" {
		token = v
	}
	if token == "" {
		// Try stored credentials for the active server
		if cfg, err := config.Load(); err == nil {
			slug := pinnedServer
			if slug == "" {
				slug = cfg.CurrentServer
			}
			if creds, err := config.LoadCredentials(slug); err == nil {
				token = creds.AccessToken
			}
		}
	}
	if token == "" {
		return fmt.Errorf("no token: set --token, ASTROLIFT_DEPLOY_TOKEN, or run `astro auth login`")
	}

	failureRate := stubFailureRate
	if v := os.Getenv("STUB_FAILURE_RATE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			failureRate = f
		}
	}

	stub := &stubDispatcher{
		apiURL:      apiURL,
		token:       token,
		name:        stubName,
		delay:       stubDelay,
		failureRate: failureRate,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		activeTasks: map[string]*stubTask{},
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	out := os.Stdout
	fmt.Fprintf(out, "Stub dispatcher %q starting\n", stub.name)
	fmt.Fprintf(out, "  Controller:   %s\n", apiURL)
	fmt.Fprintf(out, "  Delay:        %s\n", stubDelay)
	fmt.Fprintf(out, "  Failure rate: %.0f%%\n", failureRate*100)

	// Register with the Controller
	regCtx, regCancel := context.WithTimeout(ctx, 15*time.Second)
	if err := stub.register(regCtx); err != nil {
		regCancel()
		return fmt.Errorf("registration failed: %w", err)
	}
	regCancel()

	if stub.dispatcherID != "" {
		fmt.Fprintf(out, "  Dispatcher ID: %s\n", stub.dispatcherID)
	}
	fmt.Fprintln(out, "Registered. Polling for tasks... (ctrl-c to stop)")

	// Start HTTP server for log streaming (non-blocking)
	go stub.serveHTTP(ctx, out)

	// Start heartbeat loop (non-blocking)
	go stub.heartbeat(ctx)

	// Poll for tasks (blocks until ctx cancelled)
	stub.poll(ctx, out)

	fmt.Fprintln(out, "Stub dispatcher stopped.")
	return nil
}

// ---- helpers ---------------------------------------------------------------

func (s *stubDispatcher) setHeaders(r *http.Request) {
	r.Header.Set("Authorization", "Bearer "+s.token)
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("User-Agent", "astro-cli/dev (dispatch-stub)")
}

// splitPath splits a URL path into non-empty segments.
func splitPath(p string) []string {
	var parts []string
	for _, seg := range bytes.Split([]byte(p), []byte{'/'}) {
		if len(seg) > 0 {
			parts = append(parts, string(seg))
		}
	}
	return parts
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---- init ------------------------------------------------------------------

func init() {
	devDispatchStubStartCmd.Flags().IntVar(&stubPort, "port", 8765, "Port for the stub's own HTTP server (log streaming)")
	devDispatchStubStartCmd.Flags().StringVar(&stubName, "name", "local-stub", "Dispatcher name to register under")
	devDispatchStubStartCmd.Flags().DurationVar(&stubDelay, "delay", 2*time.Second, "Simulated delay between task state transitions")
	devDispatchStubStartCmd.Flags().Float64Var(&stubFailureRate, "failure-rate", 0, "Fraction of tasks to simulate as failed (0–1); overridden by STUB_FAILURE_RATE")
	devDispatchStubStartCmd.Flags().StringVar(&stubAPIURL, "api-url", "", "Controller API URL (overrides active server config and ASTROLIFT_API_URL)")
	devDispatchStubStartCmd.Flags().StringVar(&stubToken, "token", "", "API token (overrides stored credentials and ASTROLIFT_DEPLOY_TOKEN)")

	devDispatchStubCmd.AddCommand(devDispatchStubStartCmd)
	devCmd.AddCommand(devDispatchStubCmd)
	rootCmd.AddCommand(devCmd)
}
