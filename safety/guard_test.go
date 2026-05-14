package safety

import "testing"

func TestSafetyStatusIncludesConfiguredThresholds(t *testing.T) {
	g := New(Config{StreakHalfPositionAt: 10, StreakPositionScale: 0.5, StreakFreezeAt: 15, StreakFreezeTicks: 12}, nil)

	st := g.SafetyStatus()
	if st.StreakHalfPositionAt != 10 {
		t.Fatalf("StreakHalfPositionAt = %d, want 10", st.StreakHalfPositionAt)
	}
	if st.StreakPositionScale != 0.5 {
		t.Fatalf("StreakPositionScale = %v, want 0.5", st.StreakPositionScale)
	}
	if st.StreakFreezeAt != 15 {
		t.Fatalf("StreakFreezeAt = %d, want 15", st.StreakFreezeAt)
	}
}

func TestLosingStreakConfiguredHalfScaleAndFreeze(t *testing.T) {
	g := New(Config{StreakHalfPositionAt: 10, StreakPositionScale: 0.5, StreakFreezeAt: 15, StreakFreezeTicks: 12}, nil)

	for i := 0; i < 9; i++ {
		g.OnTradeClosed(-1.0)
	}
	st := g.SafetyStatus()
	if st.CurrentStreak != 9 {
		t.Fatalf("CurrentStreak = %d, want 9", st.CurrentStreak)
	}
	if st.StreakScale != 1.0 {
		t.Fatalf("StreakScale = %v, want 1.0", st.StreakScale)
	}
	if !g.AllowOpen() {
		t.Fatalf("AllowOpen() = false, want true")
	}

	g.OnTradeClosed(-1.0)
	st = g.SafetyStatus()
	if st.CurrentStreak != 10 {
		t.Fatalf("CurrentStreak = %d, want 10", st.CurrentStreak)
	}
	if st.StreakScale != 0.5 {
		t.Fatalf("StreakScale = %v, want 0.5", st.StreakScale)
	}
	if !g.AllowOpen() {
		t.Fatalf("AllowOpen() = false, want true")
	}

	for i := 0; i < 5; i++ {
		g.OnTradeClosed(-1.0)
	}
	st = g.SafetyStatus()
	if st.CurrentStreak != 15 {
		t.Fatalf("CurrentStreak = %d, want 15", st.CurrentStreak)
	}
	if st.FreezeTicksLeft != 12 {
		t.Fatalf("FreezeTicksLeft = %d, want 12", st.FreezeTicksLeft)
	}
	if st.StreakScale != 0.0 {
		t.Fatalf("StreakScale = %v, want 0.0", st.StreakScale)
	}
	if g.AllowOpen() {
		t.Fatalf("AllowOpen() = true, want false")
	}
}

func TestLosingStreakCustomPositionScale(t *testing.T) {
	g := New(Config{StreakHalfPositionAt: 2, StreakPositionScale: 0.35, StreakFreezeAt: 100, StreakFreezeTicks: 12}, nil)

	g.OnTradeClosed(-1.0)
	g.OnTradeClosed(-1.0)
	if st := g.SafetyStatus(); st.StreakScale != 0.35 {
		t.Fatalf("StreakScale = %v, want 0.35", st.StreakScale)
	}
}

func TestLosingStreakProfitResetsScale(t *testing.T) {
	g := New(Config{StreakHalfPositionAt: 2, StreakPositionScale: 0.5, StreakFreezeAt: 100}, nil)

	g.OnTradeClosed(-1.0)
	g.OnTradeClosed(-1.0)
	if st := g.SafetyStatus(); st.StreakScale != 0.5 {
		t.Fatalf("StreakScale = %v, want 0.5", st.StreakScale)
	}

	g.OnTradeClosed(1.0)
	st := g.SafetyStatus()
	if st.CurrentStreak != 0 {
		t.Fatalf("CurrentStreak = %d, want 0", st.CurrentStreak)
	}
	if st.StreakScale != 1.0 {
		t.Fatalf("StreakScale = %v, want 1.0", st.StreakScale)
	}
	if !g.AllowOpen() {
		t.Fatalf("AllowOpen() = false, want true")
	}
}
