package portfolio

import (
	"encoding/json"
	"fmt"
	"os"
)

// EquityTier defines a single equity band.
type EquityTier struct {
	MinEquity         float64 `json:"min_equity"`
	SinglePositionCap float64 `json:"single_position_cap"`
}

// EquityTiers holds the full tier configuration.
type EquityTiers struct {
	BasePositions int          `json:"base_positions"`
	Tiers         []EquityTier `json:"tiers"`
}

// LoadTiers reads and validates a tier configuration from path.
func LoadTiers(path string) (*EquityTiers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("equity_tiers: 读取 %s 失败: %w", path, err)
	}
	var t EquityTiers
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("equity_tiers: 解析 %s 失败: %w", path, err)
	}
	if t.BasePositions <= 0 {
		return nil, fmt.Errorf("equity_tiers: base_positions 必须 > 0，当前为 %d", t.BasePositions)
	}
	if len(t.Tiers) == 0 {
		return nil, fmt.Errorf("equity_tiers: tiers 数组不能为空")
	}
	for i, tier := range t.Tiers {
		if tier.MinEquity <= 0 {
			return nil, fmt.Errorf("equity_tiers: tiers[%d].min_equity 必须 > 0，当前为 %.0f", i, tier.MinEquity)
		}
		if tier.SinglePositionCap <= 0 {
			return nil, fmt.Errorf("equity_tiers: tiers[%d].single_position_cap 必须 > 0，当前为 %.0f", i, tier.SinglePositionCap)
		}
		if i > 0 && tier.MinEquity <= t.Tiers[i-1].MinEquity {
			return nil, fmt.Errorf("equity_tiers: tiers[%d].min_equity=%.0f 必须 > tiers[%d].min_equity=%.0f", i, tier.MinEquity, i-1, t.Tiers[i-1].MinEquity)
		}
	}
	return &t, nil
}

// Lookup returns the applicable tier for equity, or the highest tier if equity
// exceeds all configured tiers.  Returns false if equity is below the lowest
// tier (only possible when the config itself is empty, which is validated at
// load time).
func (t *EquityTiers) Lookup(equity float64) (EquityTier, bool) {
	if len(t.Tiers) == 0 {
		return EquityTier{}, false
	}
	// Start from the end so we short-circuit on the highest applicable tier.
	for i := len(t.Tiers) - 1; i >= 0; i-- {
		if equity >= t.Tiers[i].MinEquity {
			return t.Tiers[i], true
		}
	}
	// equity is below the lowest tier — return the lowest tier as a floor.
	return t.Tiers[0], true
}

// SingleCap returns the single-position cap for the given equity.
func (t *EquityTiers) SingleCap(equity float64) float64 {
	tier, _ := t.Lookup(equity)
	return tier.SinglePositionCap
}

// MaxPositions returns the dynamic max positions for the given equity.
//
// Formula: base_positions + floor((equity − tier.MinEquity) / tier.SinglePositionCap)
func (t *EquityTiers) MaxPositions(equity float64) int {
	tier, _ := t.Lookup(equity)
	extra := int((equity - tier.MinEquity) / tier.SinglePositionCap)
	if extra < 0 {
		extra = 0
	}
	return t.BasePositions + extra
}
