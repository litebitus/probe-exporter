// Package proberesults holds each probe's latest result and serves them: as metrics, each series labeled with its probe's identity, and as a page with each probe's log.
package proberesults

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
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

// Results are the held results, ordered by probe target, prober, address, port, host and record type.
func (store *ResultStore) Results() []probeschedule.Result {
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	return slices.SortedFunc(maps.Values(store.results), func(left, right probeschedule.Result) int {
		return cmp.Or(
			cmp.Compare(left.Identity.ProbeTarget, right.Identity.ProbeTarget),
			cmp.Compare(left.Identity.Prober, right.Identity.Prober),
			cmp.Compare(left.Identity.Target.Address, right.Identity.Target.Address),
			cmp.Compare(left.Identity.Target.Port, right.Identity.Target.Port),
			cmp.Compare(left.Identity.Target.Protocol, right.Identity.Target.Protocol),
			cmp.Compare(left.Identity.Target.Host, right.Identity.Target.Host),
			cmp.Compare(left.Identity.Target.RecordType, right.Identity.Target.RecordType),
		)
	})
}

// Gather answers every held result's series, each labeled with its identity labels where set.
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

func resultLabels(result probeschedule.Result) []*dto.LabelPair {
	values := map[string]string{
		"probe_target": result.Identity.ProbeTarget,
		"prober":       result.Identity.Prober,
		"address":      result.Identity.Target.Address,
	}
	if result.Identity.Target.Protocol != "" {
		values["port"] = strconv.Itoa(result.Identity.Target.Port) + "/" + result.Identity.Target.Protocol
	}
	if result.Identity.Target.Host != "" {
		values["host"] = result.Identity.Target.Host
	}
	if result.Identity.Target.RecordType != "" {
		values["record_type"] = result.Identity.Target.RecordType
	}
	var labels []*dto.LabelPair
	for _, name := range slices.Sorted(maps.Keys(values)) {
		labels = append(labels, &dto.LabelPair{Name: proto.String(name), Value: proto.String(values[name])})
	}
	return labels
}
