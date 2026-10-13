package usagestats

// chart_endpoints_removed_test.go locks the backend contract after the dashboard
// charts were removed: the trend endpoint is gone and the remaining payloads no
// longer carry chart-only series fields, while the surfaces the cards and both
// tables depend on stay intact.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openChartRegressionStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(Config{
		DataPath:        filepath.Join(dir, "usage-stats.db"),
		RetentionDays:   365,
		FlushInterval:   100 * time.Millisecond,
		FlushMaxRecords: 10,
		SyncOnRecord:    true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	first := NewUsage("workbuddy", "workbuddy-provider", "gpt-4o", "gpt-4o", "https://www.codebuddy.cn", "oauth", "uid-1", now.Add(-2*time.Minute), 1_500_000_000, false, 200, NewCounters(1000, 500, 0, 200, 150, 50, 0))
	if err := store.Record(first); err != nil {
		t.Fatalf("Record first: %v", err)
	}
	second := NewUsage("workbuddy", "qoder-ai-provider", "deepseek-v4.1-flash", "deepseek-v4.1-flash", "https://www.codebuddy.cn", "oauth", "uid-2", now.Add(-time.Minute), 400_000_000, false, 200, NewCounters(200, 100, 0, 0, 0, 0, 0))
	if err := store.Record(second); err != nil {
		t.Fatalf("Record second: %v", err)
	}
	return store
}

// TestTrendsRouteIsRemoved asserts /stats/trends is no longer a served route.
func TestTrendsRouteIsRemoved(t *testing.T) {
	store := openChartRegressionStore(t)

	res := store.HandleQuery(http.MethodGet, "/stats/trends", url.Values{"range": []string{"24h"}}, nil, nil)
	if res.Status != http.StatusNotFound {
		t.Fatalf("/stats/trends status=%d want 404 body=%s", res.Status, res.Body)
	}
	if !strings.Contains(string(res.Body), "route not found") {
		t.Fatalf("/stats/trends body=%s want route-not-found error", res.Body)
	}
}

// TestInitialStatsDropsChartSeries asserts /stats/initial keeps the card and
// filter surfaces but stops shipping the hourly series and bucket width.
func TestInitialStatsDropsChartSeries(t *testing.T) {
	store := openChartRegressionStore(t)

	res := store.HandleQuery(http.MethodGet, "/stats/initial", url.Values{"range": []string{"24h"}}, nil, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("/stats/initial status=%d body=%s", res.Status, res.Body)
	}
	body := string(res.Body)
	for _, forbidden := range []string{`"series"`, `"bucket_seconds"`, `"model_series"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("/stats/initial still ships chart-only field %s: %s", forbidden, body)
		}
	}

	var initial InitialStatsResponse
	if err := json.Unmarshal(res.Body, &initial); err != nil {
		t.Fatalf("/stats/initial decode: %v", err)
	}
	if initial.Summary.Requests != 2 {
		t.Errorf("Summary.Requests=%d want 2", initial.Summary.Requests)
	}
	if len(initial.Models) != 2 {
		t.Errorf("Models len=%d want 2", len(initial.Models))
	}
	if len(initial.Sources) == 0 {
		t.Error("Sources must stay available for the filter dropdown")
	}
	if initial.LastUsed.IsZero() {
		t.Error("LastUsed must stay available for the status line")
	}
}

// TestCostsDropsSeriesForSameSummary asserts /costs keeps summary and per-model
// rows (cards, coverage line and the cost column) without the series payload.
func TestCostsDropsSeriesForSameSummary(t *testing.T) {
	store := openChartRegressionStore(t)

	res := store.HandleQuery(http.MethodGet, "/costs", url.Values{"range": []string{"24h"}}, nil, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("/costs status=%d body=%s", res.Status, res.Body)
	}
	body := string(res.Body)
	if strings.Contains(body, `"series"`) {
		t.Fatalf("/costs still ships chart-only series: %s", body)
	}

	var costs CostResponse
	if err := json.Unmarshal(res.Body, &costs); err != nil {
		t.Fatalf("/costs decode: %v", err)
	}
	if costs.Summary.Requests != 2 {
		t.Errorf("costs Summary.Requests=%d want 2", costs.Summary.Requests)
	}
	if len(costs.Models) == 0 {
		t.Error("costs Models must stay populated for the cost column")
	}
}

// TestGroupsAndRequestsStayAvailable asserts the two kept tables are unaffected.
func TestGroupsAndRequestsStayAvailable(t *testing.T) {
	store := openChartRegressionStore(t)

	res := store.HandleQuery(http.MethodGet, "/stats/groups", url.Values{"range": []string{"24h"}}, nil, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("/stats/groups status=%d body=%s", res.Status, res.Body)
	}
	var groups GroupStatsPage
	if err := json.Unmarshal(res.Body, &groups); err != nil {
		t.Fatalf("/stats/groups decode: %v", err)
	}
	if groups.Total != 2 {
		t.Errorf("/stats/groups Total=%d want 2", groups.Total)
	}

	res = store.HandleQuery(http.MethodGet, "/requests", url.Values{"limit": []string{"10"}}, nil, nil)
	if res.Status != http.StatusOK {
		t.Fatalf("/requests status=%d body=%s", res.Status, res.Body)
	}
	var page RequestPage
	if err := json.Unmarshal(res.Body, &page); err != nil {
		t.Fatalf("/requests decode: %v", err)
	}
	if page.Total != 2 {
		t.Errorf("/requests Total=%d want 2", page.Total)
	}
	if len(page.Items) > 0 && page.Items[0].Provider == "" {
		t.Errorf("/requests provider column must stay populated: %+v", page.Items[0].Dimensions)
	}
}

// TestBothDashboardVariantsDropCharts guards the two rendered dashboard variants
// (public and full-mode) against silently surviving chart markup and against the
// strings.Replacer patterns losing their anchors: every removed marker must be gone
// from both variants, the kept markup must stay, and the rewritten reset-dialog
// stripping pattern must actually have matched (otherwise the replacement silently
// becomes a no-op and full-mode regains a stale variable declaration).
func TestBothDashboardVariantsDropCharts(t *testing.T) {
	variants := map[string]string{"public": dashboardHTML, "full": fullDashboardHTML}
	gone := []string{`id="tooltip"`, `id="granularity"`, `id="barWrap"`, `id="costChart"`, `id="efficiencyChart"`,
		`id="donut"`, `id="resetModelButton"`, `id="exportPNG"`, `<section class="visual-grid"`,
		`<section class="advanced-grid"`, "function renderBar(", "function renderDonut(", "function renderCostTrend(",
		"function renderEfficiency(", "function exportPNG(", "statsTrendsURL", "/stats/trends",
		"resetDialog=document.getElementById('resetDialog')", `"trend.title"`, `"modelShare.title"`}
	kept := []string{`id="groups"`, `id="requestRows"`, `id="totalTokens"`, "function renderGroups(", "function renderVisuals(",
		"{key:'provider'", "backupDialog=document.getElementById('backupDialog')"}
	// The public build strips the export script range (csvCell/exportCSV/backup) and
	// the pricing controls by design, so those markers are asserted on full-mode only.
	fullOnly := []string{"function exportCSV(", "function csvCell(", "function downloadBackup("}
	for name, html := range variants {
		if html == "" {
			t.Fatalf("%s dashboard html is empty", name)
		}
		for _, marker := range gone {
			if strings.Contains(html, marker) {
				t.Errorf("%s variant still contains %q", name, marker)
			}
		}
		for _, marker := range kept {
			if !strings.Contains(html, marker) {
				t.Errorf("%s variant lost %q", name, marker)
			}
		}
		if name == "public" {
			// Locks the existing split: the export script range is stripped from the
			// public build on purpose, so full-mode is the only CSV-capable surface.
			if strings.Contains(html, "function exportCSV(") {
				t.Errorf("public variant unexpectedly keeps exportCSV")
			}
			continue
		}
		for _, marker := range fullOnly {
			if !strings.Contains(html, marker) {
				t.Errorf("%s variant lost %q", name, marker)
			}
		}
	}
}
