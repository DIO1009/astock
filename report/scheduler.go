package report

import (
	"context"
	"fmt"
	"log"
	"time"

	"astock_trade/calendar"
	"astock_trade/store"
)

// Scheduler triggers daily report generation at a fixed time, with automatic
// retry on failure and startup/cross-day compensation.
//
// # Daily schedule
//
//	15:10:00 CST – initial attempt
//	15:11:00 CST – 1st retry  (if initial failed)
//	15:13:00 CST – 2nd retry  (if 1st retry failed)
//	15:16:00 CST – 3rd retry  (if 2nd retry failed)
//	→ status = FAILED after all retries exhausted
//
// # Startup compensation
//
//	On startup, if today's report is absent and current time > 15:10 → run immediately.
//
// # Cross-day compensation
//
//	On startup, if yesterday's report status != SUCCESS → run for yesterday immediately.
type Scheduler struct {
	gen reportGenerator
	st  reportStore
	cal tradingCalendar
	now func() time.Time
	cst *time.Location

	// schedule within each day (CST).  First entry is the initial attempt; the
	// rest are retry times (absolute clock times, not relative delays).
	schedule []timeOfDay
}

type timeOfDay struct{ hour, min int }

type reportGenerator interface {
	Generate(context.Context, time.Time) (string, error)
}

type reportStore interface {
	GetDailyReport(context.Context, time.Time) (*store.DailyReportRow, error)
	UpsertDailyReport(context.Context, store.DailyReportRow) error
}

type tradingCalendar interface {
	IsTradeDay(time.Time) bool
	NextTradeDay(time.Time) time.Time
}

// defaultSchedule: [15:10, 15:11, 15:13, 15:16]
var defaultSchedule = []timeOfDay{
	{15, 10},
	{15, 11},
	{15, 13},
	{15, 16},
}

// NewScheduler creates a Scheduler with the default 15:10 CST trigger time.
func NewScheduler(gen *Generator, st *store.Store) *Scheduler {
	return &Scheduler{
		gen:      gen,
		st:       st,
		cst:      time.FixedZone("CST", 8*3600),
		schedule: defaultSchedule,
		cal:      calendar.New(),
		now:      time.Now,
	}
}

// Run starts the report scheduler and blocks until ctx is cancelled.
// Call in a dedicated goroutine.
func (s *Scheduler) Run(ctx context.Context) {
	log.Println("[ReportScheduler] 启动（每天 15:10 CST 自动生成日报）")

	// ── Startup checks ────────────────────────────────────────────────────────
	s.startupCheck(ctx)

	// ── Daily loop ────────────────────────────────────────────────────────────
	for {
		// Wait until the initial trigger time of the next trading day.
		now := s.currentTime()
		next := s.nextReportTime(now)

		log.Printf("[ReportScheduler] 下次日报生成: %s CST", next.Format("2006-01-02 15:04"))
		select {
		case <-ctx.Done():
			log.Println("[ReportScheduler] 停止")
			return
		case <-time.After(time.Until(next)):
			s.runWithRetry(ctx, next)
		}
	}
}

// ── Startup compensation ──────────────────────────────────────────────────────

// startupCheck performs two compensations on startup:
//  1. Cross-day: if yesterday's report is missing or failed → generate now.
//  2. Same-day:  if today's report is missing and time > 15:10 → generate now.
func (s *Scheduler) startupCheck(ctx context.Context) {
	now := s.currentTime()
	today := s.dayStart(now)
	yesterday := today.AddDate(0, 0, -1)

	// ① Cross-day compensation
	if !s.isTradeDay(yesterday) {
		log.Printf("[ReportScheduler] 昨日（%s）非交易日，跳过补生成", yesterday.Format("2006-01-02"))
	} else {
		yRec, err := s.st.GetDailyReport(ctx, yesterday)
		if err != nil {
			log.Printf("[ReportScheduler] 查询昨日报告记录失败: %v", err)
		} else if yRec == nil || yRec.Status != store.ReportStatusSuccess {
			log.Printf("[ReportScheduler] 昨日（%s）报告%s，立即补生成…",
				yesterday.Format("2006-01-02"), missingOrFailed(yRec))
			s.generateForDate(ctx, yesterday, 0)
		} else {
			log.Printf("[ReportScheduler] 昨日报告 ✅ 已存在（%s）", yesterday.Format("2006-01-02"))
		}
	}

	// ② Same-day compensation: only if we're past the initial trigger time
	triggerToday := time.Date(today.Year(), today.Month(), today.Day(),
		s.schedule[0].hour, s.schedule[0].min, 0, 0, s.cst)
	if now.After(triggerToday) {
		if !s.isTradeDay(today) {
			log.Printf("[ReportScheduler] 今日（%s）非交易日，跳过补生成", today.Format("2006-01-02"))
		} else {
			tRec, err := s.st.GetDailyReport(ctx, today)
			if err != nil {
				log.Printf("[ReportScheduler] 查询今日报告记录失败: %v", err)
			} else if tRec == nil || tRec.Status != store.ReportStatusSuccess {
				log.Printf("[ReportScheduler] 今日（%s）报告%s 且已过 15:10，立即补生成…",
					today.Format("2006-01-02"), missingOrFailed(tRec))
				s.generateForDate(ctx, today, 0)
			} else {
				log.Printf("[ReportScheduler] 今日报告 ✅ 已存在（%s）", today.Format("2006-01-02"))
			}
		}
	}
}

// ── Daily run with retry ──────────────────────────────────────────────────────

// runWithRetry executes report generation for 'date' according to the retry
// schedule.  Each slot in s.schedule is an absolute clock time; the function
// blocks until a slot arrives (or gives up after all slots are exhausted).
func (s *Scheduler) runWithRetry(ctx context.Context, date time.Time) {
	day := s.dayStart(date)
	if !s.isTradeDay(day) {
		log.Printf("[ReportScheduler] %s 非交易日，跳过日报生成", day.Format("2006-01-02"))
		return
	}

	for attempt, slot := range s.schedule {
		// Wait until this slot's clock time (it may already be in the past for
		// the initial attempt since runWithRetry was called right on time).
		slotTime := time.Date(day.Year(), day.Month(), day.Day(),
			slot.hour, slot.min, 0, 0, s.cst)
		waitDur := time.Until(slotTime)
		if waitDur > 0 {
			log.Printf("[ReportScheduler] 第 %d 次尝试等待至 %s CST…",
				attempt+1, slotTime.Format("15:04"))
			select {
			case <-ctx.Done():
				return
			case <-time.After(waitDur):
			}
		}

		ok := s.generateForDate(ctx, day, attempt)
		if ok {
			return
		}
	}

	// All attempts exhausted → mark FAILED in DB
	log.Printf("[ReportScheduler] ❌ %s 日报生成失败（已重试 %d 次）",
		day.Format("2006-01-02"), len(s.schedule)-1)
	_ = s.st.UpsertDailyReport(ctx, store.DailyReportRow{
		Date:        day,
		Status:      store.ReportStatusFailed,
		GeneratedAt: time.Now(),
		ErrorMsg:    "all retry attempts exhausted",
		RetryCount:  len(s.schedule) - 1,
	})
}

// generateForDate runs the generator for the given date, records the result,
// and returns true on success.
func (s *Scheduler) generateForDate(ctx context.Context, date time.Time, retryCount int) bool {
	day := s.dayStart(date)
	if !s.isTradeDay(day) {
		log.Printf("[ReportScheduler] %s 非交易日，跳过日报生成", day.Format("2006-01-02"))
		return true
	}

	dateStr := day.Format("2006-01-02")
	log.Printf("[ReportScheduler] 生成 %s 日报（第 %d 次）…", dateStr, retryCount+1)

	// Mark as PENDING so a crash mid-generation is detectable
	_ = s.st.UpsertDailyReport(ctx, store.DailyReportRow{
		Date:       day,
		Status:     store.ReportStatusPending,
		RetryCount: retryCount,
	})

	path, err := s.gen.Generate(ctx, day)
	if err != nil {
		log.Printf("[ReportScheduler] ❌ 生成失败: %v", err)
		_ = s.st.UpsertDailyReport(ctx, store.DailyReportRow{
			Date:        day,
			Status:      store.ReportStatusFailed,
			GeneratedAt: time.Now(),
			ErrorMsg:    err.Error(),
			RetryCount:  retryCount,
		})
		return false
	}

	// Success
	_ = s.st.UpsertDailyReport(ctx, store.DailyReportRow{
		Date:        day,
		Status:      store.ReportStatusSuccess,
		ReportPath:  path,
		GeneratedAt: time.Now(),
		RetryCount:  retryCount,
	})
	log.Printf("[ReportScheduler] ✅ %s 日报已生成 → %s", dateStr, path)
	return true
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func (s *Scheduler) currentTime() time.Time {
	if s.now != nil {
		return s.now().In(s.cst)
	}
	return time.Now().In(s.cst)
}

func (s *Scheduler) dayStart(t time.Time) time.Time {
	t = t.In(s.cst)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.cst)
}

func (s *Scheduler) nextReportTime(now time.Time) time.Time {
	now = now.In(s.cst)
	trigger := s.schedule[0]
	day := s.dayStart(now)
	candidate := time.Date(day.Year(), day.Month(), day.Day(),
		trigger.hour, trigger.min, 0, 0, s.cst)
	if !candidate.After(now) {
		day = day.AddDate(0, 0, 1)
		candidate = time.Date(day.Year(), day.Month(), day.Day(),
			trigger.hour, trigger.min, 0, 0, s.cst)
	}

	for !s.isTradeDay(candidate) {
		nextTradeDay := s.dayStart(s.nextTradeDayAfter(candidate))
		candidate = time.Date(nextTradeDay.Year(), nextTradeDay.Month(), nextTradeDay.Day(),
			trigger.hour, trigger.min, 0, 0, s.cst)
	}
	return candidate
}

func (s *Scheduler) isTradeDay(t time.Time) bool {
	if s.cal != nil {
		return s.cal.IsTradeDay(t)
	}
	day := s.dayStart(t)
	weekday := day.Weekday()
	return weekday != time.Saturday && weekday != time.Sunday
}

func (s *Scheduler) nextTradeDayAfter(t time.Time) time.Time {
	if s.cal != nil {
		return s.cal.NextTradeDay(t)
	}
	for next := s.dayStart(t).AddDate(0, 0, 1); ; next = next.AddDate(0, 0, 1) {
		if s.isTradeDay(next) {
			return next
		}
	}
}

func missingOrFailed(r *store.DailyReportRow) string {
	if r == nil {
		return "（不存在）"
	}
	return fmt.Sprintf("（状态=%s）", r.Status)
}
