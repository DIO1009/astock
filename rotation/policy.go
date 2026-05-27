// Package rotation implements soft portfolio rotation policy.
package rotation

import (
	"fmt"
	"time"

	"astock_trade/core"
)

const MissingRank = 9999

const (
	BlockedOpeningWindow     = "ROTATION_BLOCKED_OPENING_WINDOW"
	BlockedRankBuffer        = "ROTATION_BLOCKED_RANK_BUFFER"
	BlockedNotConfirmed      = "ROTATION_BLOCKED_NOT_CONFIRMED"
	BlockedNoBetterCandidate = "ROTATION_BLOCKED_NO_BETTER_CANDIDATE"
	BlockedDeltaTooSmall     = "ROTATION_BLOCKED_DELTA_TOO_SMALL"
	BlockedDailyLimit        = "ROTATION_BLOCKED_DAILY_LIMIT"
	Confirmed                = "ROTATION_CONFIRMED"
)

// Config controls soft rotation thresholds.
type Config struct {
	RotationStartTime    string
	RotationWatchRank    int
	RotationExitRank     int
	RotationConfirmTicks int
	RotationConfirmDays  int
	RotationDelta        float64
	LossDeltaMultiplier  float64
	MaxRotationPerDay    int
}

// DefaultConfig returns conservative defaults for A-share live trading.
func DefaultConfig() Config {
	return Config{
		RotationStartTime:    "09:45",
		RotationWatchRank:    70,
		RotationExitRank:     85,
		RotationConfirmTicks: 3,
		RotationConfirmDays:  2,
		RotationDelta:        0.10,
		LossDeltaMultiplier:  1.5,
		MaxRotationPerDay:    3,
	}
}

// RankInfo is the current rank/score for a held symbol.
type RankInfo struct {
	Symbol string
	Rank   int
	Score  float64
}

// CandidateInfo is the best currently unheld replacement candidate.
type CandidateInfo struct {
	Symbol string
	Rank   int
	Score  float64
}

// Decision is the policy outcome for one held position.
type Decision struct {
	Rotate         bool
	Reason         string
	Candidate      CandidateInfo
	EffectiveDelta float64
	ScoreDelta     float64
	TickCount      int
	DayCount       int
	RotationsToday int
}

type symbolState struct {
	LagTicks int
	LagDays  int
	LastDay  int64
}

// Policy keeps per-symbol confirmation state and daily rotation counts.
type Policy struct {
	cfg            Config
	state          map[string]*symbolState
	currentDay     int64
	rotationsToday int
}

func New(cfg Config) *Policy {
	cfg = normalize(cfg)
	return &Policy{
		cfg:        cfg,
		state:      make(map[string]*symbolState),
		currentDay: -1,
	}
}

func normalize(cfg Config) Config {
	def := DefaultConfig()
	if cfg.RotationStartTime == "" {
		cfg.RotationStartTime = def.RotationStartTime
	}
	if cfg.RotationWatchRank <= 0 {
		cfg.RotationWatchRank = def.RotationWatchRank
	}
	if cfg.RotationExitRank <= 0 {
		cfg.RotationExitRank = def.RotationExitRank
	}
	if cfg.RotationConfirmTicks <= 0 {
		cfg.RotationConfirmTicks = def.RotationConfirmTicks
	}
	if cfg.RotationConfirmDays <= 0 {
		cfg.RotationConfirmDays = def.RotationConfirmDays
	}
	if cfg.RotationDelta <= 0 {
		cfg.RotationDelta = def.RotationDelta
	}
	if cfg.LossDeltaMultiplier <= 0 {
		cfg.LossDeltaMultiplier = def.LossDeltaMultiplier
	}
	if cfg.MaxRotationPerDay <= 0 {
		cfg.MaxRotationPerDay = def.MaxRotationPerDay
	}
	return cfg
}

// Config returns a copy of the active policy configuration.
func (p *Policy) Config() Config { return p.cfg }

// ShouldRotate decides whether a position should be rotated into candidate.
// pnlPct is expressed in percentage points, e.g. -2.3 means -2.3%.
func (p *Policy) ShouldRotate(
	pos core.Position,
	rank RankInfo,
	candidate *CandidateInfo,
	now time.Time,
	pnlPct float64,
	tradeDay int64,
) Decision {
	p.advanceDay(tradeDay)
	if rank.Rank <= 0 {
		rank.Rank = MissingRank
	}
	st := p.updateSymbolState(pos.Symbol, rank.Rank, tradeDay)

	dec := Decision{
		Reason:         BlockedRankBuffer,
		TickCount:      st.LagTicks,
		DayCount:       st.LagDays,
		RotationsToday: p.rotationsToday,
	}

	if rank.Rank <= p.cfg.RotationWatchRank {
		return dec
	}
	if rank.Rank <= p.cfg.RotationExitRank {
		return dec
	}

	if st.LagTicks < p.cfg.RotationConfirmTicks && st.LagDays < p.cfg.RotationConfirmDays {
		dec.Reason = BlockedNotConfirmed
		return dec
	}
	if candidate == nil || candidate.Symbol == "" {
		dec.Reason = BlockedNoBetterCandidate
		return dec
	}

	dec.Candidate = *candidate
	dec.EffectiveDelta = p.cfg.RotationDelta
	if pnlPct < 0 {
		dec.EffectiveDelta *= p.cfg.LossDeltaMultiplier
	}
	dec.ScoreDelta = candidate.Score - rank.Score
	if dec.ScoreDelta < dec.EffectiveDelta {
		dec.Reason = BlockedDeltaTooSmall
		return dec
	}
	if p.rotationsToday >= p.cfg.MaxRotationPerDay {
		dec.Reason = BlockedDailyLimit
		return dec
	}
	if p.inOpeningWindow(now) {
		dec.Reason = BlockedOpeningWindow
		return dec
	}

	dec.Rotate = true
	dec.Reason = Confirmed
	return dec
}

// RecordRotation increments the daily count after a ROTATION sell is confirmed.
func (p *Policy) RecordRotation(tradeDay int64) {
	p.advanceDay(tradeDay)
	p.rotationsToday++
}

func (p *Policy) advanceDay(tradeDay int64) {
	if p.currentDay == tradeDay {
		return
	}
	p.currentDay = tradeDay
	p.rotationsToday = 0
}

func (p *Policy) updateSymbolState(symbol string, rank int, tradeDay int64) *symbolState {
	st := p.state[symbol]
	if st == nil {
		st = &symbolState{}
		p.state[symbol] = st
	}

	if rank > p.cfg.RotationExitRank {
		st.LagTicks++
	} else {
		st.LagTicks = 0
	}

	if st.LastDay != tradeDay {
		st.LastDay = tradeDay
		if rank > p.cfg.RotationWatchRank {
			st.LagDays++
		} else {
			st.LagDays = 0
		}
	}
	return st
}

func (p *Policy) inOpeningWindow(now time.Time) bool {
	start, err := time.Parse("15:04", p.cfg.RotationStartTime)
	if err != nil {
		return false
	}
	clock := now.Format("15:04")
	return clock >= "09:30" && clock < start.Format("15:04")
}

func FormatSellLog(old RankInfo, pnlPct float64, cand CandidateInfo, delta float64) string {
	return fmt.Sprintf("[Rotation] SELL old=%s rank=%d score=%.4f pnl=%+.2f%%\n           BUY  new=%s rank=%d score=%.4f delta=%+.4f\n           reason=%s",
		old.Symbol, old.Rank, old.Score, pnlPct,
		cand.Symbol, cand.Rank, cand.Score, delta,
		Confirmed)
}
