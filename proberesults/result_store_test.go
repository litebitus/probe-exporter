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

func mockResult(host, address string, success bool, families ...*dto.MetricFamily) probeschedule.Result {
	return probeschedule.Result{
		Identity: probeschedule.ProbeIdentity{
			ProbeTarget: "mock-target",
			Prober:      "certificate",
			Target:      probeschedule.Target{Address: address, Port: 443, Protocol: "tcp", Host: host},
		},
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

// Every series of a result carries its run's place, in label-name order, beside the prober's own; a port, host or record type the run took none of is no label.
func TestEachSeriesCarriesItsRunsPlace(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a.example", "192.0.2.10", true, mockFamily("mock_value", 7, "mock_prober_label", "mock")))
	lookup := mockResult("mock-a.example", "192.0.2.53", true, mockFamily("mock_value", 8))
	lookup.Identity.Prober, lookup.Identity.Target.Port, lookup.Identity.Target.Protocol = "dns", 53, "udp"
	lookup.Identity.Target.RecordType = "AAAA"
	store.Keep(lookup)
	registration := mockResult("", "mock-domain.example", true, mockFamily("mock_value", 9))
	registration.Identity.Prober, registration.Identity.Target.Port, registration.Identity.Target.Protocol = "domain_registration", 0, ""
	store.Keep(registration)
	families, err := store.Gather()
	if err != nil || len(families) != 1 || len(families[0].Metric) != 3 {
		t.Fatalf("families = %v (%v)", families, err)
	}
	want := []string{
		"address=192.0.2.10,host=mock-a.example,mock_prober_label=mock,port=443/tcp,probe_target=mock-target,prober=certificate",
		"address=192.0.2.53,host=mock-a.example,port=53/udp,probe_target=mock-target,prober=dns,record_type=AAAA",
		"address=mock-domain.example,probe_target=mock-target,prober=domain_registration",
	}
	for index, metric := range families[0].Metric {
		if got := labelsOf(metric); got != want[index] {
			t.Errorf("series %d labels = %s, want %s", index, got, want[index])
		}
	}
}

// A run with every part of a place carries exactly the identity labels, so the list other packages hold info labels against is the one the store sets.
func TestTheIdentityLabelsAreTheLabelsTheStoreSets(t *testing.T) {
	store := NewResultStore()
	lookup := mockResult("mock-a.example", "192.0.2.53", true, mockFamily("mock_value", 8))
	lookup.Identity.Target.RecordType = "AAAA"
	store.Keep(lookup)
	families, _ := store.Gather()
	var set []string
	for _, label := range families[0].Metric[0].Label {
		set = append(set, label.GetName())
	}
	if strings.Join(set, ",") != strings.Join(IdentityLabels, ",") {
		t.Errorf("the store sets %v, the identity labels are %v", set, IdentityLabels)
	}
}

func TestGatheringLeavesTheHeldResultsAsTheyAre(t *testing.T) {
	store := NewResultStore()
	family := mockFamily("mock_value", 7)
	store.Keep(mockResult("mock-a.example", "192.0.2.10:443", true, family))
	store.Gather()
	if len(family.Metric[0].Label) != 0 {
		t.Errorf("the held series gained labels: %v", family.Metric[0].Label)
	}
}

func TestSeriesOfOneNameFromSeveralResultsAreOneFamilyInNameOrder(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a.example", "192.0.2.10:443", true, mockFamily("mock_z", 1), mockFamily("mock_a", 1)))
	store.Keep(mockResult("mock-b.example", "192.0.2.10:443", true, mockFamily("mock_a", 2)))
	families, _ := store.Gather()
	if len(families) != 2 || families[0].GetName() != "mock_a" || len(families[0].Metric) != 2 || families[1].GetName() != "mock_z" {
		t.Errorf("families = %v", families)
	}
}

func TestALatestResultReplacesTheOneBeforeAndARetainDropsTheRest(t *testing.T) {
	store := NewResultStore()
	first := mockResult("mock-a.example", "192.0.2.10:443", false)
	store.Keep(first)
	store.Keep(mockResult("mock-a.example", "192.0.2.10:443", true))
	store.Keep(mockResult("mock-b.example", "192.0.2.10:443", true))
	store.Retain(map[probeschedule.ProbeIdentity]bool{first.Identity: true})
	results := store.Results()
	if len(results) != 1 || !results[0].Success || results[0].Identity != first.Identity {
		t.Errorf("results = %+v", results)
	}
}

func TestResultsAreOrderedByTheirRunsPlace(t *testing.T) {
	store := NewResultStore()
	for _, run := range [][2]string{
		{"mock-b.example", "192.0.2.10"}, {"mock-a.example", "192.0.2.11"}, {"mock-a.example", "192.0.2.10"},
	} {
		store.Keep(mockResult(run[0], run[1], true))
	}
	var got []string
	for _, result := range store.Results() {
		got = append(got, result.Identity.Target.Address+"/"+result.Identity.Target.Host)
	}
	if want := "192.0.2.10/mock-a.example,192.0.2.10/mock-b.example,192.0.2.11/mock-a.example"; strings.Join(got, ",") != want {
		t.Errorf("order = %v, want %s", got, want)
	}
}

func TestTheResultsPageShowsEachRunWithItsLogEscaped(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a.example", "192.0.2.10:443", false))
	recorder := httptest.NewRecorder()
	store.ResultsPage().ServeHTTP(recorder, httptest.NewRequest("GET", "/results", nil))
	body, _ := io.ReadAll(recorder.Body)
	page := string(body)
	for _, want := range []string{
		"<td>mock-target</td>", "<td>certificate</td>", "<td>192.0.2.10:443</td>", "<td>443/tcp</td>", "<td>mock-a.example</td>",
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

// failingWriter is a response a page cannot be written to.
type failingWriter struct {
	*httptest.ResponseRecorder
}

func (writer failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestAResultsPageThatCannotBeWrittenAnswersAnError(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a.example", "192.0.2.10:443", true))
	writer := failingWriter{httptest.NewRecorder()}
	store.ResultsPage().ServeHTTP(writer, httptest.NewRequest("GET", "/results", nil))
	if writer.Code != 500 {
		t.Errorf("status = %d", writer.Code)
	}
}

func TestTheResultsPageSaysASucceededRunSucceeded(t *testing.T) {
	store := NewResultStore()
	store.Keep(mockResult("mock-a.example", "192.0.2.10:443", true))
	recorder := httptest.NewRecorder()
	store.ResultsPage().ServeHTTP(recorder, httptest.NewRequest("GET", "/results", nil))
	if !strings.Contains(recorder.Body.String(), "<td>succeeded</td>") {
		t.Errorf("page = %s", recorder.Body.String())
	}
}
