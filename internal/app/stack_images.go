package app

import (
	"reflect"
	"sort"
	"strings"
)

type ServiceImageChange struct {
	Service        string   `json:"service"`
	Before         []string `json:"before"`
	After          []string `json:"after"`
	BeforeComplete bool     `json:"beforeComplete"`
	AfterComplete  bool     `json:"afterComplete"`
	Status         string   `json:"status"`
}

type OperationImages struct {
	Services []ServiceImageChange `json:"services"`
	Warning  string               `json:"warning,omitempty"`
}

type observedServiceImages struct {
	IDs      []string
	Complete bool
}

// Reuse the exact project's container read. IDs describe observed container
// images, never inferred tag resolutions. One-off and foreign containers cannot
// contribute image IDs. Bounds keep diagnostic additions well below job limits.
func (l *stackLibrary) observeImages(record StackRecord, service string, containers []Container) map[string]observedServiceImages {
	result := map[string]observedServiceImages{}
	count := 0
	for _, item := range containers {
		if item.Stack != record.Name || (service != "" && item.Service != service) || strings.EqualFold(item.ComposeOneOff, "true") {
			continue
		}
		count++
		if count > 200 || !l.ownsContainer(record, item) {
			return nil
		}
		observed, found := result[item.Service]
		if !found {
			observed = observedServiceImages{IDs: []string{}, Complete: true}
		}
		if !comparisonDigest.MatchString(item.ImageID) {
			observed.Complete = false
		} else {
			observed.IDs = append(observed.IDs, strings.ToLower(item.ImageID))
		}
		result[item.Service] = observed
		if len(result) > 100 {
			return nil
		}
	}
	for service, observed := range result {
		sort.Strings(observed.IDs)
		unique := []string{}
		for _, id := range observed.IDs {
			if len(unique) == 0 || unique[len(unique)-1] != id {
				unique = append(unique, id)
			}
		}
		observed.IDs = unique
		result[service] = observed
	}
	return result
}

func operationImages(services []ServiceSummary, before, after map[string]observedServiceImages) *OperationImages {
	names := map[string]bool{}
	for _, service := range services {
		names[service.Name] = true
	}
	for service := range before {
		names[service] = true
	}
	for service := range after {
		names[service] = true
	}
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	result := &OperationImages{Services: []ServiceImageChange{}}
	if len(keys) > 100 {
		keys = keys[:100]
		result.Warning = "Only the first 100 services are shown."
	}
	for _, name := range keys {
		a, aPresent := before[name]
		b, bPresent := after[name]
		row := ServiceImageChange{Service: name, Before: []string{}, After: []string{}, BeforeComplete: before != nil && (!aPresent || a.Complete), AfterComplete: after != nil && (!bPresent || b.Complete), Status: "unavailable"}
		row.Before, row.After = append(row.Before, a.IDs...), append(row.After, b.IDs...)
		if row.BeforeComplete && row.AfterComplete {
			switch {
			case len(row.Before) == 0 && len(row.After) == 0:
			case len(row.Before) == 0:
				row.Status = "added"
			case len(row.After) == 0:
				row.Status = "removed"
			case reflect.DeepEqual(row.Before, row.After):
				row.Status = "unchanged"
			default:
				row.Status = "changed"
			}
		}
		result.Services = append(result.Services, row)
	}
	if before == nil || after == nil {
		result.Warning = "One image observation is unavailable or exceeded the tracking limits. Saved IDs describe only the observations that completed."
	}
	return result
}

// The existing phase/final-result write persists these fields with the job. An
// interrupted apply retains its before observation without inventing an after.
func (l *stackLibrary) setImageObservations(id string, images *OperationImages) {
	l.jobsMu.Lock()
	defer l.jobsMu.Unlock()
	job := l.jobs[id]
	job.Images = images
	l.jobs[id] = job
}
