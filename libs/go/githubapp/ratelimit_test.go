package githubapp

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestBudgetOptimisticWhenUnknown(t *testing.T) {
	b := NewBudget()
	// No observation yet → allow (optimistic), no wait.
	if wait, ok := b.Reserve(REST, 1); !ok || wait != 0 {
		t.Fatalf("unknown pool should allow: ok=%v wait=%v", ok, wait)
	}
}

func TestBudgetBlocksNearFloor(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := NewBudget()
	b.now = func() time.Time { return now }

	reset := now.Add(30 * time.Minute)
	h := http.Header{}
	h.Set("X-RateLimit-Remaining", strconv.Itoa(safetyFloor)) // at the floor
	h.Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
	b.Observe(h)

	wait, ok := b.Reserve(REST, 1)
	if ok {
		t.Fatal("at the safety floor the call must be deferred")
	}
	if wait <= 0 || wait > 30*time.Minute {
		t.Fatalf("expected wait ~30m, got %v", wait)
	}
}

func TestBudgetAllowsWithHeadroomAndDebits(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := NewBudget()
	b.now = func() time.Time { return now }

	h := http.Header{}
	h.Set("X-RateLimit-Remaining", "1000")
	h.Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(time.Hour).Unix(), 10))
	b.Observe(h)

	if _, ok := b.Reserve(REST, 5); !ok {
		t.Fatal("should allow with headroom")
	}
	if rem, known := b.Remaining(REST); !known || rem != 995 {
		t.Fatalf("expected optimistic debit to 995, got %d (known=%v)", rem, known)
	}
}

func TestBudgetResetsAfterWindow(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	b := NewBudget()
	b.now = func() time.Time { return now }

	h := http.Header{}
	h.Set("X-RateLimit-Remaining", "1") // exhausted
	h.Set("X-RateLimit-Reset", strconv.FormatInt(now.Add(time.Minute).Unix(), 10))
	b.Observe(h)
	if _, ok := b.Reserve(REST, 1); ok {
		t.Fatal("exhausted pool should block before reset")
	}

	// Advance past the reset → quota refreshed → optimistic allow again.
	now = now.Add(2 * time.Minute)
	if _, ok := b.Reserve(REST, 1); !ok {
		t.Fatal("after window reset the pool should allow again")
	}
}

func TestRegistryPerInstallation(t *testing.T) {
	reg := NewRegistry()
	if reg.For(1) == reg.For(2) {
		t.Error("distinct installations must get distinct budgets")
	}
	if reg.For(1) != reg.For(1) {
		t.Error("same installation must reuse its budget")
	}
}
