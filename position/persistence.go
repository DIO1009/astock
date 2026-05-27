// persistence.go adds SaveState / LoadState to position.Manager.
//
// The state file is a newline-terminated JSON object of the form:
//
//	{"saved_at":"2026-03-23T10:00:00+08:00","positions":[...]}
//
// Design invariants:
//   - Saving is atomic: written to a temp file, then renamed, so a crash
//     mid-write never corrupts the last good snapshot.
//   - Loading is idempotent: calling LoadState on an empty/missing file is
//     a no-op (returns nil). Existing in-memory positions are NOT cleared
//     before loading – caller should use a freshly constructed Manager.
//   - The position.Config (StopLoss/TakeProfit/Trail thresholds) is NOT
//     persisted; it is always sourced from the constructor at startup.
package position

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"astock_trade/core"
)

// positionState is the JSON schema for the persisted state file.
type positionState struct {
	SavedAt   time.Time       `json:"saved_at"`
	Positions []core.Position `json:"positions"`
}

// SaveState serialises all open positions to a JSON file at path.
//
// The write is atomic: the data is first written to path+".tmp", then
// renamed to path.  On most POSIX systems rename is atomic within the
// same filesystem.
func (m *Manager) SaveState(path string) error {
	m.mu.RLock()
	positions := make([]core.Position, 0, len(m.positions))
	for _, p := range m.positions {
		positions = append(positions, *p)
	}
	m.mu.RUnlock()

	state := positionState{
		SavedAt:   time.Now(),
		Positions: positions,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("position.SaveState marshal: %w", err)
	}

	// Write to a temp file in the same directory so rename stays on the same fs.
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".position_state_*.tmp")
	if err != nil {
		return fmt.Errorf("position.SaveState create tmp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("position.SaveState write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("position.SaveState close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("position.SaveState rename: %w", err)
	}
	return nil
}

// LoadState reads a persisted state file and populates the Manager's
// position book.  It is safe to call on a freshly constructed (empty)
// Manager.
//
// If the file does not exist, LoadState returns nil (considered a clean start).
// If the file is present but malformed, an error is returned.
//
// Positions loaded from disk are merged into the current book using the
// same semantics as ApplyTrade(BUY): existing positions are overwritten
// (direct insert, not averaged) because the saved state is already the
// authoritative weighted average.
func (m *Manager) LoadState(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // no saved state – clean start
	}
	if err != nil {
		return fmt.Errorf("position.LoadState read: %w", err)
	}

	var state positionState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("position.LoadState unmarshal: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range state.Positions {
		cp := p
		// Backward-compat: snapshot saved before T+1 feature has SellableQty=0.
		// Treat these as already-unlocked positions (they were buyable before the
		// feature was introduced, so we grant full sellability on restore).
		if cp.SellableQty == 0 && cp.Quantity > 0 {
			cp.SellableQty = cp.Quantity
		}
		m.positions[p.Symbol] = &cp
	}
	return nil
}
