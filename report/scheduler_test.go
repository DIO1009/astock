package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"astock_trade/calendar"
	"astock_trade/store"
)

var schedulerTestCST = time.FixedZone("CST", 8*3600)

func testTime(year int, month time.Month, day, hour, min int) time.Time {
	return time.Date(year, month, day, hour, min, 0, 0, schedulerTestCST)
}

func dateKey(t time.Time) string {
	return t.In(schedulerTestCST).Format("2006-01-02")
}

func sameDateTime(a, b time.Time) bool {
	return a.In(schedulerTestCST).Equal(b.In(schedulerTestCST))
}

type fakeReportGenerator struct {
	calls []time.Time
	err   error
}

func (g *fakeReportGenerator) Generate(ctx context.Context, date time.Time) (string, error) {
	g.calls = append(g.calls, date.In(schedulerTestCST))
	if g.err != nil {
		return "", g.err
	}
	return "reports/" + dateKey(date) + ".md", nil
}

type fakeReportStore struct {
	reports  map[string]*store.DailyReportRow
	getCalls []time.Time
	upserts  []store.DailyReportRow
}

func (s *fakeReportStore) GetDailyReport(ctx context.Context, date time.Time) (*store.DailyReportRow, error) {
	s.getCalls = append(s.getCalls, date.In(schedulerTestCST))
	if s.reports == nil {
		return nil, nil
	}
	row, ok := s.reports[dateKey(date)]
	if !ok {
		return nil, nil
	}
	copy := *row
	return &copy, nil
}

func (s *fakeReportStore) UpsertDailyReport(ctx context.Context, r store.DailyReportRow) error {
	s.upserts = append(s.upserts, r)
	if s.reports == nil {
		s.reports = make(map[string]*store.DailyReportRow)
	}
	copy := r
	s.reports[dateKey(r.Date)] = &copy
	return nil
}

func newTestScheduler(now time.Time, gen *fakeReportGenerator, st *fakeReportStore) *Scheduler {
	return &Scheduler{
		gen:      gen,
		st:       st,
		cst:      schedulerTestCST,
		schedule: defaultSchedule,
		cal:      calendar.New(),
		now:      func() time.Time { return now },
	}
}

func TestStartupCheckSkipsWeekendCatchUpAfter1510AndNextReportIsNextTradeDay(t *testing.T) {
	now := testTime(2026, time.May, 24, 19, 20)
	gen := &fakeReportGenerator{}
	st := &fakeReportStore{}
	s := newTestScheduler(now, gen, st)

	s.startupCheck(context.Background())

	if len(gen.calls) != 0 {
		t.Fatalf("weekend startup must not call generator; got %d calls", len(gen.calls))
	}
	if len(st.upserts) != 0 {
		t.Fatalf("weekend startup must not write PENDING/FAILED/SUCCESS reports; got %d upserts", len(st.upserts))
	}
	if len(st.getCalls) != 0 {
		t.Fatalf("non-trading days should be skipped before DB report lookup; got %d lookups", len(st.getCalls))
	}

	next := s.nextReportTime(s.currentTime())
	expected := testTime(2026, time.May, 25, 15, 10)
	if !sameDateTime(next, expected) {
		t.Fatalf("next report time = %s, want %s",
			next.In(schedulerTestCST).Format("2006-01-02 15:04 MST"),
			expected.In(schedulerTestCST).Format("2006-01-02 15:04 MST"))
	}
}

func TestNextReportTimeSkipsWeekendCandidateBeforeTrigger(t *testing.T) {
	now := testTime(2026, time.May, 22, 16, 0)
	s := newTestScheduler(now, &fakeReportGenerator{}, &fakeReportStore{})

	next := s.nextReportTime(now)
	expected := testTime(2026, time.May, 25, 15, 10)
	if !sameDateTime(next, expected) {
		t.Fatalf("next report time = %s, want %s",
			next.In(schedulerTestCST).Format("2006-01-02 15:04 MST"),
			expected.In(schedulerTestCST).Format("2006-01-02 15:04 MST"))
	}
}

func TestStartupCheckTradingDayCatchUpStillGeneratesMissingReportAfter1510(t *testing.T) {
	now := testTime(2026, time.May, 25, 19, 20)
	gen := &fakeReportGenerator{}
	st := &fakeReportStore{}
	s := newTestScheduler(now, gen, st)

	s.startupCheck(context.Background())

	if len(gen.calls) != 1 {
		t.Fatalf("expected one generator call for missing trading-day report, got %d", len(gen.calls))
	}
	if dateKey(gen.calls[0]) != "2026-05-25" {
		t.Fatalf("generator call date = %s, want 2026-05-25", dateKey(gen.calls[0]))
	}
	if len(st.upserts) < 2 {
		t.Fatalf("expected at least two upserts for 2026-05-25, got %d", len(st.upserts))
	}
	if dateKey(st.upserts[0].Date) != "2026-05-25" || st.upserts[0].Status != store.ReportStatusPending {
		t.Fatalf("first upsert = (%s, %s), want (2026-05-25, %s)", dateKey(st.upserts[0].Date), st.upserts[0].Status, store.ReportStatusPending)
	}
	last := st.upserts[len(st.upserts)-1]
	if dateKey(last.Date) != "2026-05-25" || last.Status != store.ReportStatusSuccess {
		t.Fatalf("last upsert = (%s, %s), want (2026-05-25, %s)", dateKey(last.Date), last.Status, store.ReportStatusSuccess)
	}
	for _, upsert := range st.upserts {
		if dateKey(upsert.Date) == "2026-05-24" {
			t.Fatalf("Sunday yesterday must be skipped; got upsert for 2026-05-24")
		}
	}
}

func TestTradingDayGeneratorFailureStillMarksFailed(t *testing.T) {
	failure := errors.New("integrity: integrity check failed: no equity-curve data for today (system may not have run today)")
	day := testTime(2026, time.May, 25, 0, 0)
	gen := &fakeReportGenerator{err: failure}
	st := &fakeReportStore{}
	s := newTestScheduler(day, gen, st)

	ok := s.generateForDate(context.Background(), day, 0)

	if ok {
		t.Fatalf("generateForDate returned true, want false")
	}
	if len(gen.calls) != 1 {
		t.Fatalf("expected one generator call, got %d", len(gen.calls))
	}
	if dateKey(gen.calls[0]) != "2026-05-25" {
		t.Fatalf("generator call date = %s, want 2026-05-25", dateKey(gen.calls[0]))
	}
	if len(st.upserts) == 0 {
		t.Fatalf("expected failed upsert")
	}
	final := st.upserts[len(st.upserts)-1]
	if final.Status != store.ReportStatusFailed {
		t.Fatalf("final upsert status = %s, want %s", final.Status, store.ReportStatusFailed)
	}
	if final.ErrorMsg != failure.Error() {
		t.Fatalf("final upsert error = %q, want %q", final.ErrorMsg, failure.Error())
	}
}

func TestGeneratorIntegrityRejectsMissingEquityData(t *testing.T) {
	g := &Generator{}
	data := &reportData{Date: testTime(2026, time.May, 25, 0, 0)}

	err := g.checkIntegrity(data)

	if err == nil {
		t.Fatalf("expected integrity error")
	}
	integrityErr, ok := err.(*ErrIntegrity)
	if !ok {
		t.Fatalf("error type = %T, want *ErrIntegrity", err)
	}
	const expectedReason = "no equity-curve data for today (system may not have run today)"
	if integrityErr.Reason != expectedReason {
		t.Fatalf("integrity reason = %q, want %q", integrityErr.Reason, expectedReason)
	}
}
