package main

import (
	"errors"
	"testing"
	"time"

	"astock_trade/calendar"
)

func TestNextAlphaRunTimeAfterSkipsWeekend(t *testing.T) {
	cst := alphaCST
	cal := calendar.New()
	now := time.Date(2026, 5, 23, 19, 20, 0, 0, cst) // Saturday
	got := nextAlphaRunTimeAfter(now, alphaRunHour, alphaRunMin, cst, cal)
	want := time.Date(2026, 5, 25, 9, 31, 0, 0, cst) // Monday trading day
	if !got.Equal(want) {
		t.Fatalf("nextAlphaRunTimeAfter() = %s, want %s", got, want)
	}
}

func TestPlanInitialAlphaRunSkipsNonTradeStartup(t *testing.T) {
	cst := alphaCST
	cal := calendar.New()
	now := time.Date(2026, 5, 24, 19, 20, 0, 0, cst) // Sunday
	plan := planInitialAlphaRun(time.Time{}, errors.New("no ranking"), now, cst, cal)
	if !plan.SkipNonTrade {
		t.Fatalf("SkipNonTrade = false, want true")
	}
	if plan.ShouldRun {
		t.Fatalf("ShouldRun = true on non-trading day")
	}
}

func TestPlanInitialAlphaRunWaitsUntilMarketOpenOnTradeDay(t *testing.T) {
	cst := alphaCST
	cal := calendar.New()
	now := time.Date(2026, 5, 25, 8, 30, 0, 0, cst)
	latest := time.Date(2026, 5, 22, 0, 0, 0, 0, cst)
	plan := planInitialAlphaRun(latest, nil, now, cst, cal)
	wantTradeDate := time.Date(2026, 5, 25, 0, 0, 0, 0, cst)
	wantWaitUntil := time.Date(2026, 5, 25, 9, 30, 0, 0, cst)
	if !plan.ShouldRun || plan.SkipNonTrade {
		t.Fatalf("plan = %+v, want ShouldRun=true and SkipNonTrade=false", plan)
	}
	if !plan.TradeDate.Equal(wantTradeDate) {
		t.Fatalf("TradeDate = %s, want %s", plan.TradeDate, wantTradeDate)
	}
	if !plan.WaitUntil.Equal(wantWaitUntil) {
		t.Fatalf("WaitUntil = %s, want %s", plan.WaitUntil, wantWaitUntil)
	}
}

func TestPlanInitialAlphaRunSkipsWhenRankingExistsForTradeDate(t *testing.T) {
	cst := alphaCST
	cal := calendar.New()
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, cst)
	latest := time.Date(2026, 5, 25, 0, 0, 0, 0, cst)
	plan := planInitialAlphaRun(latest, nil, now, cst, cal)
	if plan.ShouldRun || plan.SkipNonTrade {
		t.Fatalf("plan = %+v, want no startup run on existing trade-date ranking", plan)
	}
}
