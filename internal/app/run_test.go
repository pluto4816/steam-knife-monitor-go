package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pluto4816/steam-knife-monitor-go/internal/catalog"
	"github.com/pluto4816/steam-knife-monitor-go/internal/collector"
	"github.com/pluto4816/steam-knife-monitor-go/internal/store"
)

type callbackSender func(context.Context, store.Alert) error

func (sender callbackSender) Send(ctx context.Context, alert store.Alert) error {
	return sender(ctx, alert)
}

func TestRunRejectsInvalidConfig(t *testing.T) {
	valid := runtimeTestConfig()
	tests := []struct {
		name   string
		change func(*RunConfig)
	}{
		{"zero full scan interval", func(config *RunConfig) { config.FullScanInterval = 0 }},
		{"zero due poll interval", func(config *RunConfig) { config.DuePollInterval = 0 }},
		{"zero error delay", func(config *RunConfig) { config.ErrorDelay = 0 }},
		{"negative page delay", func(config *RunConfig) { config.PageDelay = -time.Second }},
		{"negative request delay", func(config *RunConfig) { config.RequestDelay = -time.Second }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			config := valid
			testCase.change(&config)
			if err := Run(context.Background(), nil, nil, config, nil); err == nil {
				t.Fatal("invalid config was accepted")
			}
		})
	}
}

func TestRunStartsWithFullScanThenProcessesDueItem(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, dueState(53, time.Now().UTC().Add(-time.Minute)))

	client, requests := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		switch {
		case path == "/api/commodity/search" && page == 1:
			return http.StatusOK, "{\"items\":[{\"id\":1}]}"
		case path == "/api/commodity/search" && page == 2:
			return http.StatusOK, "{\"items\":[]}"
		case path == "/api/commodity/exchange" && page == 1:
			return http.StatusOK, "[]"
		case method == http.MethodGet && path == "/api/commodity/53":
			return http.StatusOK, detailData(53, "0.59", 400, "2026-08-19 16:06:44")
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	config := runtimeTestConfig()
	config.RequestDelay = 15 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	if err := Run(ctx, client, stateStore, config, nil); err != nil {
		t.Fatal(err)
	}
	gotRequests := requests.snapshot()
	wantPaths := []string{
		"/api/commodity/search",
		"/api/commodity/search",
		"/api/commodity/exchange",
		"/api/commodity/53",
	}
	if len(gotRequests) < len(wantPaths) {
		t.Fatalf("requests = %#v, want at least %v", gotRequests, wantPaths)
	}
	for index, want := range wantPaths {
		if gotRequests[index].Path != want {
			t.Fatalf("request %d path = %q, want %q", index, gotRequests[index].Path, want)
		}
	}
	fullScanFinished := gotRequests[2].At
	detailStarted := gotRequests[3].At
	if detailStarted.Sub(fullScanFinished) < 10*time.Millisecond {
		t.Fatalf("full scan to due delay = %s, want request delay", detailStarted.Sub(fullScanFinished))
	}
	state, found, err := stateStore.GetItemState(context.Background(), 53)
	if err != nil || !found {
		t.Fatalf("get due state: found=%t err=%v", found, err)
	}
	if state.Ratio != 5900 || state.NextCheckAt.Before(time.Now().UTC()) {
		t.Fatalf("due state was not updated: %#v", state)
	}
	if requests.countRefresh() != 0 {
		t.Fatalf("refresh requests = %d, want 0", requests.countRefresh())
	}
}

func TestRunRepeatsFullScanAtInterval(t *testing.T) {
	client, requests := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		if path == "/api/commodity/search" {
			return http.StatusOK, "{\"items\":[]}"
		}
		return http.StatusInternalServerError, "{}"
	})
	config := runtimeTestConfig()
	config.FullScanInterval = 20 * time.Millisecond
	config.DuePollInterval = 5 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 85*time.Millisecond)
	defer cancel()

	if err := Run(ctx, client, openTestStore(t), config, nil); err != nil {
		t.Fatal(err)
	}
	if count := requests.countPath("/api/commodity/search"); count < 2 {
		t.Fatalf("full scan requests = %d, want at least 2", count)
	}
}

func TestRunFullScanErrorStillProcessesDueItem(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, dueState(53, time.Now().UTC().Add(-time.Minute)))

	client, requests := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		switch path {
		case "/api/commodity/search":
			return http.StatusInternalServerError, "{}"
		case "/api/commodity/53":
			return http.StatusOK, detailData(53, "0.59", 400, "2026-08-19 16:06:44")
		default:
			return http.StatusInternalServerError, "{}"
		}
	})
	config := runtimeTestConfig()
	config.ErrorDelay = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
	defer cancel()

	if err := Run(ctx, client, stateStore, config, nil); err != nil {
		t.Fatal(err)
	}
	if requests.countPath("/api/commodity/search") != 1 {
		t.Fatalf("full scan requests = %d, want 1", requests.countPath("/api/commodity/search"))
	}
	if requests.countPath("/api/commodity/53") != 1 {
		t.Fatalf("detail requests = %d, want 1", requests.countPath("/api/commodity/53"))
	}
	state, found, err := stateStore.GetItemState(context.Background(), 53)
	if err != nil || !found || state.Ratio != 5900 {
		t.Fatalf("due state after failed scan: state=%#v found=%t err=%v", state, found, err)
	}
}

func TestRunFullScanFailureUsesErrorDelay(t *testing.T) {
	client, requests := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		return http.StatusInternalServerError, "{}"
	})
	config := runtimeTestConfig()
	config.DuePollInterval = 4 * time.Millisecond
	config.ErrorDelay = 40 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 115*time.Millisecond)
	defer cancel()

	if err := Run(ctx, client, openTestStore(t), config, nil); err != nil {
		t.Fatal(err)
	}
	count := requests.countPath("/api/commodity/search")
	if count < 2 || count > 4 {
		t.Fatalf("full scan requests = %d, want 2..4 with error delay", count)
	}
}

func TestRunIncompleteTargetedScanBlocksDueChecksAndNotificationsUntilResumeCompletes(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, dueState(3, time.Now().UTC().Add(-time.Minute)))

	itemTwoAttempts := 0
	client, requests := runtimeTestClient(t, func(_ string, path string, _ int) (int, string) {
		switch path {
		case "/api/commodity/1":
			return http.StatusOK, detailData(1, "0.59", 400, "2026-09-02 01:00:00")
		case "/api/commodity/2":
			itemTwoAttempts++
			if itemTwoAttempts == 1 {
				return http.StatusInternalServerError, `{}`
			}
			return http.StatusOK, detailData(2, "0.65", 300, "2026-09-02 01:00:01")
		case "/api/commodity/3":
			return http.StatusOK, detailData(3, "0.70", 300, "2026-09-02 01:00:02")
		default:
			return http.StatusInternalServerError, `{}`
		}
	})
	config := runtimeTestConfig()
	config.Candidates = []catalog.Item{
		{ItemID: 1, MarketHashName: "hash-1", NameCN: "一"},
		{ItemID: 2, MarketHashName: "hash-2", NameCN: "二"},
		{ItemID: 3, MarketHashName: "hash-3", NameCN: "三"},
	}
	config.ErrorDelay = 25 * time.Millisecond
	config.DuePollInterval = 5 * time.Millisecond

	sendCount := 0
	sentBeforeCompleteScan := false
	sender := callbackSender(func(_ context.Context, _ store.Alert) error {
		sendCount++
		if requests.countPath("/api/commodity/2") < 2 || requests.countPath("/api/commodity/3") < 1 {
			sentBeforeCompleteScan = true
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	if err := Run(ctx, client, stateStore, config, sender); err != nil {
		t.Fatal(err)
	}
	if sentBeforeCompleteScan {
		t.Fatal("notification was sent before the resumed targeted scan completed")
	}
	if sendCount != 1 {
		t.Fatalf("notification sends = %d, want 1 after the complete scan", sendCount)
	}
	gotRequests := requests.snapshot()
	wantPaths := []string{
		"/api/commodity/1",
		"/api/commodity/2",
		"/api/commodity/2",
		"/api/commodity/3",
	}
	if len(gotRequests) != len(wantPaths) {
		t.Fatalf("requests = %#v, want exactly %v", gotRequests, wantPaths)
	}
	for index, want := range wantPaths {
		if gotRequests[index].Path != want {
			t.Fatalf("request %d path = %q, want %q", index, gotRequests[index].Path, want)
		}
	}
	if retryGap := gotRequests[2].At.Sub(gotRequests[1].At); retryGap < 20*time.Millisecond {
		t.Fatalf("targeted scan retry gap = %s, want approximately ErrorDelay", retryGap)
	}
	if _, found, err := stateStore.GetTargetedScanCheckpoint(context.Background()); err != nil || found {
		t.Fatalf("completed targeted scan checkpoint found=%t err=%v", found, err)
	}
}

func TestRunDueFailureUsesErrorDelay(t *testing.T) {
	stateStore := openTestStore(t)
	saveState(t, stateStore, dueState(53, time.Now().UTC().Add(-time.Minute)))

	client, requests := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		if path == "/api/commodity/search" {
			return http.StatusOK, "{\"items\":[]}"
		}
		if path == "/api/commodity/53" {
			return http.StatusInternalServerError, "{}"
		}
		return http.StatusInternalServerError, "{}"
	})
	config := runtimeTestConfig()
	config.DuePollInterval = time.Millisecond
	config.ErrorDelay = 35 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Millisecond)
	defer cancel()

	if err := Run(ctx, client, stateStore, config, nil); err != nil {
		t.Fatal(err)
	}
	detailTimes := requests.timesForPath("/api/commodity/53")
	if len(detailTimes) < 2 || len(detailTimes) > 5 {
		t.Fatalf("detail requests = %d, want 2..5 with error delay", len(detailTimes))
	}
	for index := 1; index < len(detailTimes); index++ {
		if gap := detailTimes[index].Sub(detailTimes[index-1]); gap < 30*time.Millisecond {
			t.Fatalf("detail retry gap = %s, want approximately ErrorDelay", gap)
		}
	}
}

func TestRunStopsOnAccessRestriction(t *testing.T) {
	client, requests := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		return http.StatusForbidden, "{}"
	})

	err := Run(context.Background(), client, openTestStore(t), runtimeTestConfig(), nil)
	if !errors.Is(err, collector.ErrAccessRestricted) {
		t.Fatalf("error = %v, want ErrAccessRestricted", err)
	}
	if requests.countPath("/api/commodity/search") != 1 || requests.total() != 1 {
		t.Fatalf("requests after restriction = %#v", requests.snapshot())
	}
}

func TestRunContextCancellationReturnsNil(t *testing.T) {
	client, _ := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		if path == "/api/commodity/search" {
			return http.StatusOK, "{\"items\":[]}"
		}
		return http.StatusInternalServerError, "{}"
	})
	config := runtimeTestConfig()
	config.DuePollInterval = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	if err := Run(ctx, client, openTestStore(t), config, nil); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestRunSendsDurablePendingAlerts(t *testing.T) {
	stateStore := openTestStore(t)
	ctx := context.Background()
	if _, err := ProcessMarketItem(ctx, stateStore, marketItem(53, 5900, 400), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	client, _ := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		if path == "/api/commodity/search" {
			return http.StatusOK, "{\"items\":[]}"
		}
		return http.StatusInternalServerError, "{}"
	})
	sender := &recordingSender{}
	runCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := Run(runCtx, client, stateStore, runtimeTestConfig(), sender); err != nil {
		t.Fatal(err)
	}
	if len(sender.alerts) != 1 {
		t.Fatalf("sent alert count = %d, want 1", len(sender.alerts))
	}
	alertContext, err := stateStore.GetAlertContext(context.Background(), 53)
	if err != nil {
		t.Fatal(err)
	}
	if alertContext.HasPending || !alertContext.HasLastSent || alertContext.LastSentRatio != 5900 {
		t.Fatalf("alert context after runtime send = %#v", alertContext)
	}
}

func TestRunNotificationFailureUsesErrorDelayAndKeepsMonitoring(t *testing.T) {
	stateStore := openTestStore(t)
	if _, err := ProcessMarketItem(context.Background(), stateStore, marketItem(53, 5900, 400), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	client, requests := runtimeTestClient(t, func(method, path string, page int) (int, string) {
		if path == "/api/commodity/search" {
			return http.StatusOK, "{\"items\":[]}"
		}
		return http.StatusInternalServerError, "{}"
	})
	sender := &recordingSender{err: errors.New("notification unavailable")}
	config := runtimeTestConfig()
	config.FullScanInterval = 15 * time.Millisecond
	config.DuePollInterval = 5 * time.Millisecond
	config.ErrorDelay = 45 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 115*time.Millisecond)
	defer cancel()

	if err := Run(ctx, client, stateStore, config, sender); err != nil {
		t.Fatal(err)
	}
	if calls := len(sender.alerts); calls < 2 || calls > 4 {
		t.Fatalf("notification attempts = %d, want 2..4 with error delay", calls)
	}
	if scans := requests.countPath("/api/commodity/search"); scans < 2 {
		t.Fatalf("full scans = %d, want monitoring to continue after sender errors", scans)
	}
	if got := len(pendingAlerts(t, stateStore)); got != 1 {
		t.Fatalf("pending alerts after sender errors = %d, want 1", got)
	}
}

func runtimeTestConfig() RunConfig {
	return RunConfig{
		FullScanInterval: time.Hour,
		DuePollInterval:  time.Hour,
		PageDelay:        0,
		RequestDelay:     0,
		ErrorDelay:       time.Hour,
	}
}

type runtimeResponse func(method, path string, page int) (status int, data string)

type runtimeRequest struct {
	Path string
	At   time.Time
}

type runtimeRequestLog struct {
	mu       sync.Mutex
	requests []runtimeRequest
}

func runtimeTestClient(t *testing.T, respond runtimeResponse) (*collector.SMISClient, *runtimeRequestLog) {
	t.Helper()
	requests := &runtimeRequestLog{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.record(request.URL.Path)
		if request.URL.Path == "/api/commodity/refresh" ||
			strings.Contains(request.URL.Path, "/refresh/status") {
			t.Errorf("runtime called refresh endpoint %s", request.URL.Path)
			response.WriteHeader(http.StatusInternalServerError)
			return
		}

		page := 0
		if request.Method == http.MethodPost {
			var body map[string]any
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&body); err != nil {
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			number, ok := body["pageNum"].(json.Number)
			if !ok {
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			parsed, err := strconv.Atoi(number.String())
			if err != nil {
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			page = parsed
		}

		status, data := respond(request.Method, request.URL.Path, page)
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(status)
		if status >= 200 && status < 300 {
			_, _ = fmt.Fprintf(response, "{\"code\":200,\"message\":\"success\",\"data\":%s}", data)
			return
		}
		_, _ = fmt.Fprint(response, data)
	}))
	t.Cleanup(server.Close)
	return collector.NewSMISClient(server.URL, server.Client()), requests
}

func (requests *runtimeRequestLog) record(path string) {
	requests.mu.Lock()
	defer requests.mu.Unlock()
	requests.requests = append(requests.requests, runtimeRequest{Path: path, At: time.Now()})
}

func (requests *runtimeRequestLog) snapshot() []runtimeRequest {
	requests.mu.Lock()
	defer requests.mu.Unlock()
	return append([]runtimeRequest(nil), requests.requests...)
}

func (requests *runtimeRequestLog) total() int {
	return len(requests.snapshot())
}

func (requests *runtimeRequestLog) countPath(path string) int {
	count := 0
	for _, request := range requests.snapshot() {
		if request.Path == path {
			count++
		}
	}
	return count
}

func (requests *runtimeRequestLog) timesForPath(path string) []time.Time {
	times := []time.Time{}
	for _, request := range requests.snapshot() {
		if request.Path == path {
			times = append(times, request.At)
		}
	}
	return times
}

func (requests *runtimeRequestLog) countRefresh() int {
	count := 0
	for _, request := range requests.snapshot() {
		if request.Path == "/api/commodity/refresh" ||
			strings.Contains(request.Path, "/refresh/status") {
			count++
		}
	}
	return count
}
