// Package dynamic provides a Screener that reads the current Top-N stock
// candidates from the PostgreSQL alpha_rankings table (written by the
// daily_alpha job).
//
// If the DB is unavailable or the table is empty, it falls back to the
// configured default symbols so the system can still trade.
//
// Refresh logic:
//   - Results are cached in memory with a configurable TTL (default 1 h).
//   - The cache is also refreshed automatically at next-day open (09:30 CST).
//   - Call Screen() on every engine tick; it is fast (returns cached slice).
package dynamic

import (
	"context"
	"log"
	"sync"
	"time"
)

// SymbolStore is the minimal DB interface required by this screener.
// Implemented by *store.Store.
type SymbolStore interface {
	GetTopSymbols(ctx context.Context, n int) ([]string, error)
}

// Screener satisfies core.Screener by querying alpha_rankings.
type Screener struct {
	db             SymbolStore
	topN           int
	fallback       []string // used when DB is unavailable or empty
	cacheTTL       time.Duration
	mu             sync.RWMutex
	cached         []string
	cachedAt       time.Time
}

// Option is a functional option for Screener.
type Option func(*Screener)

// WithFallback sets the default symbol list used when no DB rankings exist.
func WithFallback(symbols []string) Option {
	return func(s *Screener) {
		cp := make([]string, len(symbols))
		copy(cp, symbols)
		s.fallback = cp
	}
}

// WithCacheTTL overrides the default 1-hour cache TTL.
func WithCacheTTL(d time.Duration) Option {
	return func(s *Screener) { s.cacheTTL = d }
}

// New creates a dynamic Screener.
//   - db    – store.Store (or any SymbolStore)
//   - topN  – how many top symbols to return per Screen() call
//   - opts  – optional configuration
func New(db SymbolStore, topN int, opts ...Option) *Screener {
	s := &Screener{
		db:       db,
		topN:     topN,
		cacheTTL: 1 * time.Hour,
	}
	for _, o := range opts {
		o(s)
	}
	// Warm cache immediately (non-fatal on error).
	s.refresh()
	return s
}

// Screen returns the current Top-N symbols.
// Results are served from cache; a background refresh fires when stale.
func (s *Screener) Screen() []string {
	s.mu.RLock()
	stale := time.Since(s.cachedAt) > s.cacheTTL
	cp := make([]string, len(s.cached))
	copy(cp, s.cached)
	s.mu.RUnlock()

	if stale {
		// Refresh in background to avoid blocking the engine tick.
		go s.refresh()
	}
	return cp
}

// ForceRefresh synchronously reloads symbols from the DB.
// Call this at market open to pick up the latest daily_alpha results.
func (s *Screener) ForceRefresh() {
	s.refresh()
}

// refresh reads the DB and updates the cache.
func (s *Screener) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	syms, err := s.db.GetTopSymbols(ctx, s.topN)
	if err != nil {
		log.Printf("[dynamic.Screener] DB 查询失败，保留旧缓存: %v", err)
		return
	}
	if len(syms) == 0 {
		log.Printf("[dynamic.Screener] alpha_rankings 为空，使用 fallback 列表 (%d 只)", len(s.fallback))
		s.mu.Lock()
		if len(s.cached) == 0 { // only apply fallback if cache is also empty
			s.cached = append([]string(nil), s.fallback...)
		}
		s.cachedAt = time.Now()
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	s.cached = syms
	s.cachedAt = time.Now()
	s.mu.Unlock()
	log.Printf("[dynamic.Screener] 候选池刷新: %d 只股票 (Top-%d)", len(syms), s.topN)
}
