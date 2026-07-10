package adapters

import (
	"context"
	"sort"
	"time"
)

// Registry holds the configured adapters and answers capability/health queries.
type Registry struct {
	byName      map[string]Adapter
	order       []string
	maxParallel int // bound on concurrent adapter calls (SPEC §7.3)
}

// NewRegistry builds a registry from a set of adapters (last-wins on name).
func NewRegistry(as ...Adapter) *Registry {
	r := &Registry{byName: make(map[string]Adapter), maxParallel: 3}
	for _, a := range as {
		if a == nil {
			continue
		}
		if _, seen := r.byName[a.Name()]; !seen {
			r.order = append(r.order, a.Name())
		}
		r.byName[a.Name()] = a
	}
	return r
}

// SetMaxParallel bounds concurrent adapter fan-out (SPEC §7.3 max_parallel_calls).
// A value < 1 is ignored (keeps the default).
func (r *Registry) SetMaxParallel(n int) {
	if n >= 1 {
		r.maxParallel = n
	}
}

// SetMaxAutoRetries threads budget.max_auto_retries_per_tool into every
// registered adapter that shells out (SPEC §17.3). Adapters without an exec
// path (fakes, git-free stubs) are skipped. Negative values are ignored.
func (r *Registry) SetMaxAutoRetries(n int) {
	if n < 0 {
		return
	}
	for _, a := range r.byName {
		if rc, ok := a.(interface{ SetMaxAutoRetries(int) }); ok {
			rc.SetMaxAutoRetries(n)
		}
	}
}

// Get returns the adapter with the given name, or nil.
func (r *Registry) Get(name string) Adapter { return r.byName[name] }

// Names returns adapter names in registration order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// WithCapability returns adapters that advertise the given capability.
func (r *Registry) WithCapability(c Capability) []Adapter {
	var out []Adapter
	for _, name := range r.order {
		a := r.byName[name]
		for _, cap := range a.Capabilities() {
			if cap == c {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// HealthReport is the per-tool health snapshot used during orientation.
type HealthReport struct {
	Tool      string `json:"tool"`
	Available bool   `json:"available"`
	Detail    string `json:"detail,omitempty"`
}

// Health probes every adapter concurrently with a short per-tool budget and
// returns a stable, name-sorted report (SPEC §6.2 orienting → tool health known).
func (r *Registry) Health(ctx context.Context) []HealthReport {
	type res struct {
		name string
		err  error
	}
	ch := make(chan res, len(r.order))
	// Bound concurrency to max_parallel_calls (SPEC §7.3) so probing many tools
	// doesn't spawn an unbounded burst of subprocesses.
	sem := make(chan struct{}, max(1, r.maxParallel))
	for _, name := range r.order {
		go func(name string, a Adapter) {
			sem <- struct{}{}
			defer func() { <-sem }()
			c, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			ch <- res{name: name, err: a.Health(c)}
		}(name, r.byName[name])
	}
	reps := make([]HealthReport, 0, len(r.order))
	for range r.order {
		x := <-ch
		hr := HealthReport{Tool: x.name, Available: x.err == nil}
		if x.err != nil {
			hr.Detail = x.err.Error()
		}
		reps = append(reps, hr)
	}
	sort.Slice(reps, func(i, j int) bool { return reps[i].Tool < reps[j].Tool })
	return reps
}
