// Package proberesults holds each probe's latest result and serves them: as metrics, each series labeled with its probe's identity, and as a page with each probe's log.
package proberesults

import (
	"cmp"
	"maps"
	"slices"
	"sync"

	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"

	"github.com/litebitus/probe-exporter/probeschedule"
)

// ResultStore is the latest result of each probe.
type ResultStore struct {
	mutex   sync.RWMutex
	results map[probeschedule.ProbeIdentity]probeschedule.Result
}

// NewResultStore is a store holding no result yet.
func NewResultStore() *ResultStore {
	return &ResultStore{results: map[probeschedule.ProbeIdentity]probeschedule.Result{}}
}

// Keep replaces a probe's result with its latest.
func (store *ResultStore) Keep(result probeschedule.Result) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.results[result.Identity] = result
}

// Retain drops the result of every probe not among the identities.
func (store *ResultStore) Retain(identities map[probeschedule.ProbeIdentity]bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for identity := range store.results {
		if !identities[identity] {
			delete(store.results, identity)
		}
	}
}

// Results are the held results, ordered by probe, target group, name and address.
func (store *ResultStore) Results() []probeschedule.Result {
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return slices.SortedFunc(maps.Values(store.results), func(left, right probeschedule.Result) int {
		return cmp.Or(
			cmp.Compare(left.Identity.Probe, right.Identity.Probe),
			cmp.Compare(left.Identity.TargetGroup, right.Identity.TargetGroup),
			cmp.Compare(left.Identity.Target.Name, right.Identity.Target.Name),
			cmp.Compare(left.Identity.Target.Address, right.Identity.Target.Address),
		)
	})
}

// Gather answers every held result's series, each labeled with its identity labels where set, and its group's labels.
func (store *ResultStore) Gather() ([]*dto.MetricFamily, error) {
	families := map[string]*dto.MetricFamily{}
	for _, result := range store.Results() {
		identityLabels := resultLabels(result)
		for _, family := range result.Families {
			gathered, known := families[family.GetName()]
			if !known {
				gathered = &dto.MetricFamily{Name: family.Name, Help: family.Help, Type: family.Type, Unit: family.Unit}
				families[family.GetName()] = gathered
			}
			for _, metric := range family.Metric {
				labeled := proto.Clone(metric).(*dto.Metric)
				labeled.Label = append(labeled.Label, identityLabels...)
				slices.SortFunc(labeled.Label, func(left, right *dto.LabelPair) int {
					return cmp.Compare(left.GetName(), right.GetName())
				})
				gathered.Metric = append(gathered.Metric, labeled)
			}
		}
	}
	return slices.SortedFunc(maps.Values(families), func(left, right *dto.MetricFamily) int {
		return cmp.Compare(left.GetName(), right.GetName())
	}), nil
}

// IdentityLabels are the labels every series of a result carries to name its run's place, which a group's labels cannot name.
var IdentityLabels = []string{"address", "name", "probe", "target_group"}

func resultLabels(result probeschedule.Result) []*dto.LabelPair {
	values := maps.Clone(result.Labels)
	if values == nil {
		values = map[string]string{}
	}
	values["probe"] = result.Identity.Probe
	values["target_group"] = result.Identity.TargetGroup
	values["name"] = result.Identity.Target.Name
	values["address"] = result.Identity.Target.Address
	var labels []*dto.LabelPair
	for _, name := range slices.Sorted(maps.Keys(values)) {
		labels = append(labels, &dto.LabelPair{Name: proto.String(name), Value: proto.String(values[name])})
	}
	return labels
}
