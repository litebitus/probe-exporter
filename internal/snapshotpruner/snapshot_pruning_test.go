package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var mockNow = time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)

// mockVersion is a package version the mock registry holds, and the platform images it lists when it is an index.
type mockVersion struct {
	id        int64
	digest    string
	tags      []string
	age       time.Duration
	platforms []string
}

// mockGitHub answers GitHub's package API and the registry's token and manifest endpoints for one package, as GitHub documents them.
type mockGitHub struct {
	server       *httptest.Server
	mutex        sync.Mutex
	versions     []mockVersion
	deleted      []int64
	failListing  bool
	failManifest bool
	failDelete   bool
}

func newMockGitHub(t *testing.T, versions []mockVersion) *mockGitHub {
	github := &mockGitHub{versions: versions}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orgs/mock-org/packages/container/mock-exporter/versions", func(writer http.ResponseWriter, request *http.Request) {
		if github.failListing || request.Header.Get("Authorization") != "Bearer mock-token" {
			http.Error(writer, "mock", http.StatusForbidden)
			return
		}
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		var answers []map[string]any
		for index, version := range github.versions {
			if index/100 != page-1 {
				continue
			}
			answers = append(answers, map[string]any{
				"id": version.id, "name": version.digest, "created_at": mockNow.Add(-version.age).Format(time.RFC3339),
				"metadata": map[string]any{"container": map[string]any{"tags": version.tags}},
			})
		}
		json.NewEncoder(writer).Encode(answers)
	})
	mux.HandleFunc("DELETE /orgs/mock-org/packages/container/mock-exporter/versions/{id}", func(writer http.ResponseWriter, request *http.Request) {
		if github.failDelete {
			http.Error(writer, "mock", http.StatusForbidden)
			return
		}
		id, _ := strconv.ParseInt(request.PathValue("id"), 10, 64)
		github.mutex.Lock()
		github.deleted = append(github.deleted, id)
		github.mutex.Unlock()
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /token", func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("scope") != "repository:mock-org/mock-exporter:pull" {
			http.Error(writer, "mock", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(writer, `{"token":"mock-registry-token"}`)
	})
	mux.HandleFunc("GET /v2/mock-org/mock-exporter/manifests/{digest}", func(writer http.ResponseWriter, request *http.Request) {
		if github.failManifest || request.Header.Get("Authorization") != "Bearer mock-registry-token" {
			http.Error(writer, "mock", http.StatusUnauthorized)
			return
		}
		for _, version := range github.versions {
			if version.digest == request.PathValue("digest") {
				var manifests []map[string]string
				for _, platform := range version.platforms {
					manifests = append(manifests, map[string]string{"digest": platform})
				}
				json.NewEncoder(writer).Encode(map[string]any{"manifests": manifests})
				return
			}
		}
		http.NotFound(writer, request)
	})
	github.server = httptest.NewServer(mux)
	t.Cleanup(github.server.Close)
	return github
}

func (github *mockGitHub) run(arguments ...string) (string, error) {
	output := &strings.Builder{}
	err := run(context.Background(), arguments, "mock-token", output, github.server.Client(), github.server.URL, github.server.URL, mockNow)
	return output.String(), err
}

const twoWeeks = 14 * 24 * time.Hour

func mockVersions() []mockVersion {
	return []mockVersion{
		{id: 1, digest: "sha256:old-snapshot", tags: []string{"0.0.1-snapshot.20261101000000"}, age: twoWeeks + time.Hour,
			platforms: []string{"sha256:old-amd64", "sha256:shared-arm64"}},
		{id: 2, digest: "sha256:old-amd64", age: twoWeeks + time.Hour},
		{id: 3, digest: "sha256:shared-arm64", age: twoWeeks + time.Hour},
		{id: 4, digest: "sha256:new-snapshot", tags: []string{"0.0.2-snapshot.20261129000000"}, age: time.Hour,
			platforms: []string{"sha256:new-amd64"}},
		{id: 5, digest: "sha256:new-amd64", age: time.Hour},
		{id: 6, digest: "sha256:release", tags: []string{"0.0.1"}, age: 30 * 24 * time.Hour,
			platforms: []string{"sha256:release-amd64", "sha256:shared-arm64"}},
		{id: 7, digest: "sha256:release-amd64", age: 30 * 24 * time.Hour},
		{id: 8, digest: "sha256:orphan", age: 60 * 24 * time.Hour},
	}
}

// An expired snapshot goes with the platform images only it lists; a newer snapshot, a release and what they list stay.
func TestAnExpiredSnapshotGoesWithThePlatformImagesOnlyItLists(t *testing.T) {
	github := newMockGitHub(t, mockVersions())
	output, err := github.run("--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(github.deleted, []int64{1, 2}) {
		t.Errorf("deleted = %v, want the old snapshot and its own platform image", github.deleted)
	}
	if output != "deleted sha256:old-snapshot\ndeleted sha256:old-amd64\n" {
		t.Errorf("output = %q", output)
	}
}

func TestADryRunNamesWhatItWouldDeleteAndDeletesNothing(t *testing.T) {
	github := newMockGitHub(t, mockVersions())
	output, err := github.run("--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14", "--dry-run")
	if err != nil || len(github.deleted) != 0 || output != "would delete sha256:old-snapshot\nwould delete sha256:old-amd64\n" {
		t.Errorf("deleted %v, output %q (%v)", github.deleted, output, err)
	}
}

func TestASnapshotTaggedAlsoAsAReleaseIsKept(t *testing.T) {
	versions := mockVersions()
	versions[0].tags = append(versions[0].tags, "0.0.1")
	github := newMockGitHub(t, versions)
	if _, err := github.run("--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14"); err != nil || len(github.deleted) != 0 {
		t.Errorf("deleted %v (%v)", github.deleted, err)
	}
}

func TestEveryPageOfVersionsIsRead(t *testing.T) {
	versions := mockVersions()
	for id := int64(100); id < 200; id++ {
		versions = append([]mockVersion{{id: id, digest: fmt.Sprintf("sha256:filler-%d", id), age: time.Hour}}, versions...)
	}
	github := newMockGitHub(t, versions)
	if _, err := github.run("--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14"); err != nil ||
		!slices.Equal(github.deleted, []int64{1, 2}) {
		t.Errorf("deleted %v (%v)", github.deleted, err)
	}
}

func TestAFailureStopsThePruning(t *testing.T) {
	for name, fail := range map[string]func(*mockGitHub){
		"listing":  func(github *mockGitHub) { github.failListing = true },
		"manifest": func(github *mockGitHub) { github.failManifest = true },
		"deleting": func(github *mockGitHub) { github.failDelete = true },
	} {
		github := newMockGitHub(t, mockVersions())
		fail(github)
		if _, err := github.run("--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14"); err == nil {
			t.Errorf("%s: the failure was not answered", name)
		}
	}
	github := newMockGitHub(t, mockVersions())
	output := &strings.Builder{}
	if err := run(context.Background(), []string{"--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14"},
		"mock-wrong-token", output, github.server.Client(), github.server.URL, github.server.URL, mockNow); err == nil {
		t.Error("a refused token was not answered")
	}
}

// unreachableRequests is a transport through which requests to one path, or of one method, never arrive.
type unreachableRequests struct {
	next   http.RoundTripper
	path   string
	method string
}

func (transport unreachableRequests) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path == transport.path || request.Method == transport.method {
		return nil, fmt.Errorf("mock unreachable")
	}
	return transport.next.RoundTrip(request)
}

func TestARequestThatNeverArrivesStopsThePruning(t *testing.T) {
	for name, transport := range map[string]unreachableRequests{
		"the registry's token": {path: "/token"},
		"a delete":             {method: http.MethodDelete},
	} {
		github := newMockGitHub(t, mockVersions())
		transport.next = github.server.Client().Transport
		client := &http.Client{Transport: transport}
		err := run(context.Background(), []string{"--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14"},
			"mock-token", &strings.Builder{}, client, github.server.URL, github.server.URL, mockNow)
		if err == nil || !strings.Contains(err.Error(), "mock unreachable") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	github := newMockGitHub(t, mockVersions())
	err := run(context.Background(), []string{"--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14"},
		"mock-token", &strings.Builder{}, github.server.Client(), "http://[::1", github.server.URL, mockNow)
	if err == nil {
		t.Error("a malformed API address was used")
	}
	registry := githubContainerRegistry{client: github.server.Client(), apiURL: "http://[::1", owner: "mock-org", packageName: "mock-exporter"}
	if err := registry.DeleteVersion(context.Background(), 1); err == nil {
		t.Error("a delete through a malformed API address was sent")
	}
}

func TestAPlatformImageTheIndexNoLongerHoldsIsSkipped(t *testing.T) {
	versions := mockVersions()
	versions[0].platforms = append(versions[0].platforms, "sha256:already-gone")
	github := newMockGitHub(t, versions)
	if _, err := github.run("--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14"); err != nil ||
		!slices.Equal(github.deleted, []int64{1, 2}) {
		t.Errorf("deleted %v (%v)", github.deleted, err)
	}
}

func TestThePrunerNeedsItsPackageAnAgeAndAToken(t *testing.T) {
	github := newMockGitHub(t, mockVersions())
	for name, testCase := range map[string]struct {
		arguments []string
		token     string
		want      string
	}{
		"no package":   {[]string{"--owner", "mock-org", "--max-age-days", "14"}, "mock-token", "--owner and --package are required"},
		"no age":       {[]string{"--owner", "mock-org", "--package", "mock-exporter"}, "mock-token", "--max-age-days is required, at least 1"},
		"no token":     {[]string{"--owner", "mock-org", "--package", "mock-exporter", "--max-age-days", "14"}, "", "GITHUB_TOKEN is required"},
		"unknown flag": {[]string{"--mock"}, "mock-token", "flag provided but not defined: -mock"},
	} {
		err := run(context.Background(), testCase.arguments, testCase.token, &strings.Builder{}, github.server.Client(),
			github.server.URL, github.server.URL, mockNow)
		if err == nil || err.Error() != testCase.want {
			t.Errorf("%s: err = %v, want %q", name, err, testCase.want)
		}
	}
}
