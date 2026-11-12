package proberesults

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"

	"github.com/litebitus/probe-exporter/probeschedule"
)

func mockFamily(name string, value float64, labels ...string) *dto.MetricFamily {
	metric := &dto.Metric{Gauge: &dto.Gauge{Value: proto.Float64(value)}}
	for index := 0; index < len(labels); index += 2 {
		metric.Label = append(metric.Label, &dto.LabelPair{Name: proto.String(labels[index]), Value: proto.String(labels[index+1])})
	}
	return &dto.MetricFamily{Name: proto.String(name), Help: proto.String("A mock series"), Type: dto.MetricType_GAUGE.Enum(), Metric: []*dto.Metric{metric}}
}

func mockResult(target, hostname, queryName string, success bool, families ...*dto.MetricFamily) probeschedule.Result {
	return probeschedule.Result{
		Identity: probeschedule.ProbeIdentity{
			Module:      "mock_module",
			TargetGroup: "mock-group",
			Target:      probeschedule.Target{Address: target, Hostname: hostname, QueryName: queryName},
		},
		Labels:   map[string]string{"mock_site": "mock-site"},
		Success:  success,
		Duration: 1500 * time.Millisecond,
		EndedAt:  time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC),
		Families: families,
		Log:      "mock log line <script>\n",
	}
}

func labelsOf(metric *dto.Metric) string {
	var pairs []string
	for _, label := range metric.Label {
		pairs = append(pairs, label.GetName()+"="+label.GetValue())
	}
	return strings.Join(pairs, ",")
}

// Every series of a result carries its probe's identity and its group's labels, in label-name order, a hostname and a query name only where set.
func TestEachSeriesCarriesItsProbesIdentityAndItsGroupsLabels(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a:443", "mock-a.example", "", true, mockFamily("mock_value", 7, "mock_prober_label", "mock")))
	store.Keep(mockResult("mock-b:53", "", "mock-name.example", true, mockFamily("mock_value", 8)))
	families, err := store.Gather()
	if err != nil || len(families) != 1 || len(families[0].Metric) != 2 {
		t.Fatalf("families = %v (%v)", families, err)
	}
	want := []string{
		"hostname=mock-a.example,mock_prober_label=mock,mock_site=mock-site,module=mock_module,target=mock-a:443,target_group=mock-group",
		"mock_site=mock-site,module=mock_module,query_name=mock-name.example,target=mock-b:53,target_group=mock-group",
	}
	for index, metric := range families[0].Metric {
		if got := labelsOf(metric); got != want[index] {
			t.Errorf("series %d labels = %s, want %s", index, got, want[index])
		}
	}
}

func TestGatheringLeavesTheHeldResultsAsTheyAre(t *testing.T) {
	store := NewResultStore()
	family := mockFamily("mock_value", 7)
	store.Keep(mockResult("mock-a:443", "", "", true, family))
	store.Gather()
	if len(family.Metric[0].Label) != 0 {
		t.Errorf("the held series gained labels: %v", family.Metric[0].Label)
	}
}

func TestSeriesOfOneNameFromSeveralResultsAreOneFamilyInNameOrder(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a:443", "", "", true, mockFamily("mock_z", 1), mockFamily("mock_a", 1)))
	store.Keep(mockResult("mock-b:443", "", "", true, mockFamily("mock_a", 2)))
	families, _ := store.Gather()
	if len(families) != 2 || families[0].GetName() != "mock_a" || len(families[0].Metric) != 2 || families[1].GetName() != "mock_z" {
		t.Errorf("families = %v", families)
	}
}

func TestALatestResultReplacesTheOneBeforeAndARetainDropsTheRest(t *testing.T) {
	store := NewResultStore()
	first := mockResult("mock-a:443", "", "", false)
	store.Keep(first)
	store.Keep(mockResult("mock-a:443", "", "", true))
	store.Keep(mockResult("mock-b:443", "", "", true))
	store.Retain(map[probeschedule.ProbeIdentity]bool{first.Identity: true})
	results := store.Results()
	if len(results) != 1 || !results[0].Success || results[0].Identity != first.Identity {
		t.Errorf("results = %+v", results)
	}
}

func TestResultsAreOrderedByTheirProbesIdentity(t *testing.T) {
	store := NewResultStore()
	for _, target := range []string{"mock-c:443", "mock-a:443", "mock-b:443"} {
		store.Keep(mockResult(target, "", "", true))
	}
	store.Keep(mockResult("mock-a:443", "mock-b.example", "", true))
	store.Keep(mockResult("mock-a:443", "mock-a.example", "", true))
	var got []string
	for _, result := range store.Results() {
		got = append(got, result.Identity.Target.Address+"/"+result.Identity.Target.Hostname)
	}
	if want := "mock-a:443/,mock-a:443/mock-a.example,mock-a:443/mock-b.example,mock-b:443/,mock-c:443/"; strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

func TestTheResultsPageShowsEachProbeWithItsLogEscaped(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a:443", "mock-a.example", "", false))
	recorder := httptest.NewRecorder()
	store.ResultsPage().ServeHTTP(recorder, httptest.NewRequest("GET", "/results", nil))
	body, _ := io.ReadAll(recorder.Body)
	page := string(body)
	for _, want := range []string{
		"<td>mock_module</td>", "<td>mock-group</td>", "<td>mock-a:443</td>", "<td>mock-a.example</td>",
		`<span class="failed">failed</span>`, "2026-11-11T10:00:00Z", "<td>1.5s</td>", "mock log line &lt;script&gt;",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page holds no %q", want)
		}
	}
	if recorder.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("content type = %s", recorder.Header().Get("Content-Type"))
	}
}

func TestTheResultsPageSaysASucceededProbeSucceeded(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a:443", "", "", true))
	recorder := httptest.NewRecorder()
	store.ResultsPage().ServeHTTP(recorder, httptest.NewRequest("GET", "/results", nil))
	if !strings.Contains(recorder.Body.String(), "<td>succeeded</td>") {
		t.Errorf("page = %s", recorder.Body.String())
	}
}
