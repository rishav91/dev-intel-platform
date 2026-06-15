package githubapp

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Resource is a GitHub rate-limit pool. REST and GraphQL are budgeted
// separately (REST ~5k req/h, GraphQL ~5k points/h per installation —
// nfr-and-capacity.md §1/§1a).
type Resource string

const (
	REST    Resource = "rest"
	GraphQL Resource = "graphql"
)

// safetyFloor leaves headroom so concurrent in-flight calls don't overshoot a
// pool to exhaustion (GitHub starts returning 403s at zero). When remaining is
// at/below the floor we wait for the window reset instead of issuing the call.
const safetyFloor = 50

// Budget tracks one installation's remaining quota per resource and decides
// whether a call may proceed now or must wait for the reset. Safe for
// concurrent use. State is fed from response headers (REST) / response body
// (GraphQL cost) via Observe / ObserveGraphQL.
type Budget struct {
	mu  sync.Mutex
	now func() time.Time

	pools map[Resource]*pool
}

type pool struct {
	remaining int
	resetAt   time.Time
	known     bool // false until the first observation
}

// NewBudget creates an empty budget (optimistic until the first observation).
func NewBudget() *Budget {
	return &Budget{now: time.Now, pools: map[Resource]*pool{
		REST:    {},
		GraphQL: {},
	}}
}

func (b *Budget) get(r Resource) *pool {
	p, ok := b.pools[r]
	if !ok {
		p = &pool{}
		b.pools[r] = p
	}
	return p
}

// Reserve reports whether a call against r may proceed now. If the pool is at or
// below the safety floor and the window hasn't reset, it returns ok=false plus
// how long to wait. Optimistically decrements the local estimate so bursts
// between header refreshes don't overshoot. cost is the call's point cost (1 for
// REST; the GraphQL query's computed cost otherwise).
func (b *Budget) Reserve(r Resource, cost int) (wait time.Duration, ok bool) {
	if cost < 1 {
		cost = 1
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.get(r)
	now := b.now()

	// Window elapsed → quota has refreshed; clear the stale view.
	if p.known && !p.resetAt.IsZero() && now.After(p.resetAt) {
		p.known = false
	}
	if !p.known {
		// Haven't observed (or just reset): allow, track the optimistic spend.
		p.remaining -= cost
		return 0, true
	}
	if p.remaining-cost < safetyFloor {
		w := p.resetAt.Sub(now)
		if w < 0 {
			w = 0
		}
		return w, false
	}
	p.remaining -= cost
	return 0, true
}

// Observe updates the REST pool from a response's rate-limit headers. No-op if
// the headers are absent (e.g. a non-API response).
func (b *Budget) Observe(h http.Header) {
	rem, ok1 := atoi(h.Get("X-RateLimit-Remaining"))
	reset, ok2 := atoi(h.Get("X-RateLimit-Reset"))
	if !ok1 || !ok2 {
		return
	}
	b.set(REST, rem, time.Unix(int64(reset), 0))
}

// ObserveGraphQL updates the GraphQL pool from a query's rateLimit block.
func (b *Budget) ObserveGraphQL(remaining int, resetAt time.Time) {
	b.set(GraphQL, remaining, resetAt)
}

func (b *Budget) set(r Resource, remaining int, resetAt time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.get(r)
	p.remaining = remaining
	p.resetAt = resetAt
	p.known = true
}

// Remaining returns the last-known remaining quota for r (and whether it has
// been observed yet) — for health metrics/alerts (FR-2.8).
func (b *Budget) Remaining(r Resource) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.get(r)
	return p.remaining, p.known
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Registry holds one Budget per installation id.
type Registry struct {
	mu      sync.Mutex
	budgets map[int64]*Budget
}

// NewRegistry creates an empty budget registry.
func NewRegistry() *Registry { return &Registry{budgets: make(map[int64]*Budget)} }

// For returns (creating if needed) the budget for an installation.
func (reg *Registry) For(installationID int64) *Budget {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	b, ok := reg.budgets[installationID]
	if !ok {
		b = NewBudget()
		reg.budgets[installationID] = b
	}
	return b
}
