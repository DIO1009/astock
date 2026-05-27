package paper

import "testing"

func TestNextOrderIDIsUniqueAcrossCalls(t *testing.T) {
	b := New(nil, 100000)

	first := b.nextOrderID()
	second := b.nextOrderID()

	if first == second {
		t.Fatalf("expected distinct order IDs, got %q", first)
	}
	if len(first) <= len("ORD--000001") {
		t.Fatalf("expected session-scoped order ID, got %q", first)
	}
}
