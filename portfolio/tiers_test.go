package portfolio

import (
	"os"
	"path/filepath"
	"testing"
)

func validTiersJSON() string {
	return `{
  "base_positions": 10,
  "tiers": [
    {"min_equity": 100000, "single_position_cap": 20000},
    {"min_equity": 200000, "single_position_cap": 40000},
    {"min_equity": 300000, "single_position_cap": 60000}
  ]
}`
}

func writeTempTiers(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "equity_tiers.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadTiers_Valid(t *testing.T) {
	path := writeTempTiers(t, validTiersJSON())
	tiers, err := LoadTiers(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tiers.BasePositions != 10 {
		t.Errorf("BasePositions = %d, want 10", tiers.BasePositions)
	}
	if len(tiers.Tiers) != 3 {
		t.Fatalf("len(Tiers) = %d, want 3", len(tiers.Tiers))
	}
	if tiers.Tiers[0].MinEquity != 100000 {
		t.Errorf("Tiers[0].MinEquity = %f, want 100000", tiers.Tiers[0].MinEquity)
	}
}

func TestLoadTiers_FileNotFound(t *testing.T) {
	_, err := LoadTiers("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadTiers_InvalidJSON(t *testing.T) {
	path := writeTempTiers(t, `{invalid}`)
	_, err := LoadTiers(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestLoadTiers_MissingBasePositions(t *testing.T) {
	path := writeTempTiers(t, `{"base_positions": 0, "tiers": [{"min_equity": 100000, "single_position_cap": 20000}]}`)
	_, err := LoadTiers(path)
	if err == nil {
		t.Fatal("expected error for base_positions <= 0")
	}
}

func TestLoadTiers_EmptyTiers(t *testing.T) {
	path := writeTempTiers(t, `{"base_positions": 10, "tiers": []}`)
	_, err := LoadTiers(path)
	if err == nil {
		t.Fatal("expected error for empty tiers")
	}
}

func TestLoadTiers_NonMonotonic(t *testing.T) {
	path := writeTempTiers(t, `{
  "base_positions": 10,
  "tiers": [
    {"min_equity": 200000, "single_position_cap": 40000},
    {"min_equity": 100000, "single_position_cap": 20000}
  ]
}`)
	_, err := LoadTiers(path)
	if err == nil {
		t.Fatal("expected error for non-monotonic tiers")
	}
}

func TestLookup_ExactBoundary(t *testing.T) {
	path := writeTempTiers(t, validTiersJSON())
	tiers, err := LoadTiers(path)
	if err != nil {
		t.Fatal(err)
	}

	tier, ok := tiers.Lookup(100000)
	if !ok {
		t.Fatal("expected tier found")
	}
	if tier.MinEquity != 100000 {
		t.Errorf("MinEquity = %f, want 100000", tier.MinEquity)
	}
	if tier.SinglePositionCap != 20000 {
		t.Errorf("SinglePositionCap = %f, want 20000", tier.SinglePositionCap)
	}
}

func TestLookup_MidRange(t *testing.T) {
	path := writeTempTiers(t, validTiersJSON())
	tiers, _ := LoadTiers(path)

	tier, ok := tiers.Lookup(150000)
	if !ok {
		t.Fatal("expected tier found")
	}
	// Should be in the 100k-200k tier
	if tier.MinEquity != 100000 {
		t.Errorf("MinEquity = %f, want 100000", tier.MinEquity)
	}
}

func TestLookup_AboveMaxTier(t *testing.T) {
	path := writeTempTiers(t, validTiersJSON())
	tiers, _ := LoadTiers(path)

	tier, ok := tiers.Lookup(500000)
	if !ok {
		t.Fatal("expected tier found")
	}
	// Should use highest tier (300k)
	if tier.MinEquity != 300000 {
		t.Errorf("MinEquity = %f, want 300000", tier.MinEquity)
	}
}

func TestLookup_BelowMinTier(t *testing.T) {
	path := writeTempTiers(t, validTiersJSON())
	tiers, _ := LoadTiers(path)

	tier, ok := tiers.Lookup(50000)
	if !ok {
		t.Fatal("expected tier found (floor)")
	}
	// Should return lowest tier as floor
	if tier.MinEquity != 100000 {
		t.Errorf("MinEquity = %f, want 100000 (floor)", tier.MinEquity)
	}
}

func TestMaxPositions_Basic(t *testing.T) {
	path := writeTempTiers(t, validTiersJSON())
	tiers, _ := LoadTiers(path)

	// 权益 10 万：base=10 + floor((100k-100k)/20k) = 10
	if n := tiers.MaxPositions(100000); n != 10 {
		t.Errorf("equity=100k: MaxPositions=%d, want 10", n)
	}

	// 权益 12 万：base=10 + floor((120k-100k)/20k) = 10+1=11
	if n := tiers.MaxPositions(120000); n != 11 {
		t.Errorf("equity=120k: MaxPositions=%d, want 11", n)
	}

	// 权益 20 万：tier=200k-300k, base=10 + floor((200k-200k)/40k) = 10
	if n := tiers.MaxPositions(200000); n != 10 {
		t.Errorf("equity=200k: MaxPositions=%d, want 10", n)
	}

	// 权益 22 万：tier=200k-300k, base=10 + floor((220k-200k)/40k) = 10+0=10
	if n := tiers.MaxPositions(220000); n != 10 {
		t.Errorf("equity=220k: MaxPositions=%d, want 10", n)
	}

	// 权益 24 万：tier=200k-300k, base=10 + floor((240k-200k)/40k) = 10+1=11
	if n := tiers.MaxPositions(240000); n != 11 {
		t.Errorf("equity=240k: MaxPositions=%d, want 11", n)
	}

	// 权益 30 万：tier=300k+, base=10 + floor((300k-300k)/60k) = 10
	if n := tiers.MaxPositions(300000); n != 10 {
		t.Errorf("equity=300k: MaxPositions=%d, want 10", n)
	}

	// 权益 36 万：tier=300k+, base=10 + floor((360k-300k)/60k) = 10+1=11
	if n := tiers.MaxPositions(360000); n != 11 {
		t.Errorf("equity=360k: MaxPositions=%d, want 11", n)
	}
}

func TestSingleCap(t *testing.T) {
	path := writeTempTiers(t, validTiersJSON())
	tiers, _ := LoadTiers(path)

	if cap := tiers.SingleCap(100000); cap != 20000 {
		t.Errorf("equity=100k: SingleCap=%f, want 20000", cap)
	}
	if cap := tiers.SingleCap(120000); cap != 20000 {
		t.Errorf("equity=120k: SingleCap=%f, want 20000", cap)
	}
	if cap := tiers.SingleCap(200000); cap != 40000 {
		t.Errorf("equity=200k: SingleCap=%f, want 40000", cap)
	}
	if cap := tiers.SingleCap(300000); cap != 60000 {
		t.Errorf("equity=300k: SingleCap=%f, want 60000", cap)
	}
	if cap := tiers.SingleCap(500000); cap != 60000 {
		t.Errorf("equity=500k: SingleCap=%f, want 60000", cap)
	}
}
