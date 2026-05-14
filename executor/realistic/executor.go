// Package realistic provides a production-grade simulated Executor that models
// real A-share trading friction as accurately as possible without a live broker.
//
// v2 升级：真实市场行为建模
//
// # 执行延迟 (Feature 1)
//
//	每笔订单模拟 MinDelayMs～MaxDelayMs 的随机执行延迟。
//	延迟期间价格按布朗运动漂移（PriceVolPerMs × √delayMs），
//	对 BUY 只计正向漂移（价格上涨导致成本升高），对 SELL 只计负向漂移。
//
// # 订单簿撮合模型 (Feature 2)
//
//	PassiveOrderRatio 比例的订单被视为被动挂单（限价单），
//	其成交概率随市场波动率上升而下降（HighVolPassiveMult 放大拒绝率），
//	高波动期整体成交率降至 65-75%，正常市场约 85-90%。
//
// # 流动性冲击 (Feature 3)
//
//	市场冲击滑点 ∝ sqrt(订单金额 / 市场成交额)，使用 MarketImpactCoeff 系数。
//	典型 A 股大单（¥3万 / ¥500万日成交）约额外增加 0.25-0.60% 滑点。
//
// # 成本模型
//
//	BUY  fill = delayed_price × (1 + base_slip + rand_slip + market_impact) + fee_per_share
//	SELL fill = delayed_price × (1 − base_slip − rand_slip − market_impact) − fee_per_share
//
//	费用按成交金额计算后折算进成交价：手续费买卖 0.0235% 且最低 5 元，
//	印花税卖出 0.05%，过户费买卖 0.001%。
package realistic

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"sync"
	"time"

	"astock_trade/core"
)

// ─── Config ───────────────────────────────────────────────────────────────────

// Config holds all parameters for the realistic execution simulator.
type Config struct {
	// ── Trading costs ───────────────────────────────────────────────────────
	CommissionPct   float64 // broker commission charged on BUY and SELL; default 0.000235 (0.0235%)
	StampTaxPct     float64 // stamp tax charged on SELL only; default 0.0005 (0.05%)
	TransferFeePct  float64 // transfer fee charged on BUY and SELL; default 0.00001 (0.001%)
	MinCommission  float64 // minimum broker commission per order; default 5.0

	// ── Slippage ────────────────────────────────────────────────────────────
	SlippageBasePct  float64 // deterministic base; default 0.001 (0.1%)
	SlippageRandPct  float64 // max random component; default 0.002 (0.2%)
	DelaySlippagePct float64 // legacy tick-delay slip; default 0.001 (kept for compat)

	// ── Liquidity cap ───────────────────────────────────────────────────────
	VolumeCapPct float64 // max fraction of tick volume per order; default 0.05 (5%)

	// ── Partial fill ────────────────────────────────────────────────────────
	PartialFillProb     float64 // probability of partial fill; default 0.20
	PartialFillMinRatio float64 // min fill ratio when partial; default 0.50

	// ── Rejection probabilities ─────────────────────────────────────────────
	RejectProbNormal  float64 // base rejection prob; default 0.06 (6%)
	RejectProbHighVol float64 // rejection prob under high volatility; default 0.22 (22%)
	HighVolThreshold  float64 // |PctChg| % that triggers high-vol mode; default 5.0

	// ── Feature 1: Execution delay ──────────────────────────────────────────
	// Simulates the latency between signal generation and actual fill.
	// During the delay, the price drifts via Brownian motion.
	MinDelayMs   float64 // minimum simulated delay (ms); default 50
	MaxDelayMs   float64 // maximum simulated delay (ms); default 500
	PriceVolPerMs float64 // intraday price vol per ms (fraction); default 0.000025
	// Derivation: daily vol ≈ 2%, 1 trading day ≈ 4h = 14400s = 14400000ms
	// vol_per_ms = 0.02 / sqrt(14400000) ≈ 0.0000053; we use a more conservative 0.000025

	// ── Feature 2: Order-book / passive fill model ───────────────────────────
	// A fraction of each order is treated as a passive (limit) order placed at
	// the current best bid/ask.  Passive orders have a lower fill probability
	// that is further reduced under high volatility (wider spreads, faster moves).
	PassiveOrderRatio  float64 // fraction treated as passive; default 0.40
	PassiveRejectProb  float64 // per-unit passive rejection probability; default 0.15
	HighVolPassiveMult float64 // multiplier on PassiveRejectProb during high-vol; default 2.5

	// ── Feature 3: Market impact ────────────────────────────────────────────
	// Additional slippage proportional to sqrt(orderValue / marketTurnover).
	// Models liquidity consumption cost for larger orders.
	// impact_pct = MarketImpactCoeff × sqrt(order_value / market_turnover)
	MarketImpactCoeff float64 // default 0.10
}

// Default returns updated production-simulation values for China A-shares.
// v2 calibration targets 70-90% fill rate and realistic 50-500ms execution delays.
func Default() Config {
	return Config{
		CommissionPct:       0.000235,
		StampTaxPct:         0.0005,
		TransferFeePct:      0.00001,
		MinCommission:       5.0,
		SlippageBasePct:     0.001,
		SlippageRandPct:     0.002,
		DelaySlippagePct:    0.001,
		VolumeCapPct:        0.05,
		PartialFillProb:     0.20,
		PartialFillMinRatio: 0.50,
		// ↑ from 0.02/0.12 → targets blended fill rate ~83%
		RejectProbNormal:  0.08,
		RejectProbHighVol: 0.25,
		HighVolThreshold:  5.0,
		// Feature 1: delay
		MinDelayMs:    50.0,
		MaxDelayMs:    500.0,
		PriceVolPerMs: 0.000025,
		// Feature 2: passive order book
		PassiveOrderRatio:  0.40,
		PassiveRejectProb:  0.15,
		HighVolPassiveMult: 2.5,
		// Feature 3: market impact
		MarketImpactCoeff: 0.10,
	}
}

// ─── Executor ─────────────────────────────────────────────────────────────────

// Executor satisfies core.Executor with full v2 trading-cost modelling.
type Executor struct {
	mu  sync.Mutex
	rng *rand.Rand
	cfg Config

	// lastDelayMs stores the simulated delay of the most recent Execute call.
	// Accessible via LastDelayMs() for logging/testing.
	lastDelayMs float64
}

// New returns a realistic Executor.  Zero-value fields in cfg are replaced by
// the defaults from Default().
func New(cfg Config) *Executor {
	d := Default()
	if cfg.CommissionPct == 0 {
		cfg.CommissionPct = d.CommissionPct
	}
	if cfg.StampTaxPct == 0 {
		cfg.StampTaxPct = d.StampTaxPct
	}
	if cfg.TransferFeePct == 0 {
		cfg.TransferFeePct = d.TransferFeePct
	}
	if cfg.MinCommission == 0 {
		cfg.MinCommission = d.MinCommission
	}
	if cfg.SlippageBasePct == 0 {
		cfg.SlippageBasePct = d.SlippageBasePct
	}
	if cfg.SlippageRandPct == 0 {
		cfg.SlippageRandPct = d.SlippageRandPct
	}
	if cfg.DelaySlippagePct == 0 {
		cfg.DelaySlippagePct = d.DelaySlippagePct
	}
	if cfg.VolumeCapPct == 0 {
		cfg.VolumeCapPct = d.VolumeCapPct
	}
	if cfg.PartialFillMinRatio == 0 {
		cfg.PartialFillMinRatio = d.PartialFillMinRatio
	}
	if cfg.HighVolThreshold == 0 {
		cfg.HighVolThreshold = d.HighVolThreshold
	}
	if cfg.MinDelayMs == 0 {
		cfg.MinDelayMs = d.MinDelayMs
	}
	if cfg.MaxDelayMs == 0 {
		cfg.MaxDelayMs = d.MaxDelayMs
	}
	if cfg.PriceVolPerMs == 0 {
		cfg.PriceVolPerMs = d.PriceVolPerMs
	}
	if cfg.PassiveOrderRatio == 0 {
		cfg.PassiveOrderRatio = d.PassiveOrderRatio
	}
	if cfg.PassiveRejectProb == 0 {
		cfg.PassiveRejectProb = d.PassiveRejectProb
	}
	if cfg.HighVolPassiveMult == 0 {
		cfg.HighVolPassiveMult = d.HighVolPassiveMult
	}
	if cfg.MarketImpactCoeff == 0 {
		cfg.MarketImpactCoeff = d.MarketImpactCoeff
	}
	return &Executor{
		rng: rand.New(rand.NewSource(time.Now().UnixNano())),
		cfg: cfg,
	}
}

// Execute simulates a realistic A-share market fill.
//
// Pipeline (v2):
//
//	 1. Basic sanity (qty > 0, price > 0)
//	 2. Circuit-breaker (A-share ±10% daily limit)
//	 3. Feature 1 – simulate execution delay → compute delayed fill price
//	 4. Probabilistic rejection (base + passive order-book model; Feature 2)
//	 5. Volume cap (≤ VolumeCapPct × tick_volume)
//	 6. Partial fill (probability + order-book passive fraction; Feature 2)
//	 7. Feature 3 – market impact slippage ∝ sqrt(order_value/market_turnover)
//	 8. Fill price with all frictions applied
func (e *Executor) Execute(order *core.Order, quote *core.Quote) (*core.Trade, error) {
	if order.Quantity <= 0 {
		return nil, fmt.Errorf("realistic: invalid quantity %d for %s", order.Quantity, order.Symbol)
	}
	if order.Price <= 0 {
		return nil, fmt.Errorf("realistic: invalid price %.4f for %s", order.Price, order.Symbol)
	}

	// ── 1. Circuit-breaker ─────────────────────────────────────────────────
	if quote.PrevClose > 0 {
		upper := quote.PrevClose * 1.10
		lower := quote.PrevClose * 0.90
		if order.Price > upper || order.Price < lower {
			return nil, fmt.Errorf("realistic: %s price %.4f outside daily limit [%.4f, %.4f]",
				order.Symbol, order.Price, lower, upper)
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	highVol := math.Abs(quote.PctChg) > e.cfg.HighVolThreshold

	// ── 2. Feature 1: Execution delay & price drift ────────────────────────
	// Simulate Brownian motion of price during the execution delay window.
	// Only adverse moves increase the effective fill price.
	delayMs := e.cfg.MinDelayMs + e.rng.Float64()*(e.cfg.MaxDelayMs-e.cfg.MinDelayMs)
	e.lastDelayMs = delayMs

	drift := e.rng.NormFloat64() * e.cfg.PriceVolPerMs * math.Sqrt(delayMs)
	// BUY is hurt when price rises; SELL is hurt when price falls.
	var delayPriceFactor float64
	switch order.Side {
	case "BUY":
		delayPriceFactor = math.Max(0, drift) // only adverse (upward) drift hurts BUY
	case "SELL":
		delayPriceFactor = math.Min(0, drift) // only adverse (downward) drift hurts SELL
	}
	delayedPrice := order.Price * (1 + delayPriceFactor)

	// ── 3. Feature 2: Order-book rejection model ───────────────────────────
	// Combined rejection = base direct rejection + passive-portion rejection.
	baseRejectProb := e.cfg.RejectProbNormal
	if highVol {
		baseRejectProb = e.cfg.RejectProbHighVol
	}

	// Passive portion rejection: a fraction PassiveOrderRatio of the order is
	// treated as a limit order; it fails to fill if the market moves too fast.
	passiveReject := e.cfg.PassiveRejectProb
	if highVol {
		passiveReject *= e.cfg.HighVolPassiveMult
	}
	// Combined rejection: base OR passive rejection fire.
	// P(reject) = base + passive × passive_ratio × (1 - base) ≈ additive for small probs.
	combinedRejectProb := baseRejectProb + e.cfg.PassiveOrderRatio*passiveReject*(1-baseRejectProb)

	if e.rng.Float64() < combinedRejectProb {
		return nil, fmt.Errorf("realistic: %s order rejected (rejectProb=%.0f%%, highVol=%v, delay=%.0fms)",
			order.Symbol, combinedRejectProb*100, highVol, delayMs)
	}

	// ── 4. Volume cap ─────────────────────────────────────────────────────
	fillQty := order.Quantity
	if quote.Volume > 0 && e.cfg.VolumeCapPct > 0 {
		maxQty := int(float64(quote.Volume) * e.cfg.VolumeCapPct)
		if maxQty < 1 {
			maxQty = 1
		}
		if fillQty > maxQty {
			fillQty = maxQty
			log.Printf("  💧 [Realistic] %-8s vol-cap: qty %d→%d (vol=%d)",
				order.Symbol, order.Quantity, fillQty, quote.Volume)
		}
	}

	// ── 5. Feature 2: Partial fill (including passive-order effect) ────────
	// In high-vol markets, passive orders are more likely to receive partial fills.
	partialProb := e.cfg.PartialFillProb
	if highVol {
		// High-vol: wider spreads → passive orders queue behind better prices
		partialProb = math.Min(0.80, partialProb+e.cfg.PassiveOrderRatio*passiveReject*0.5)
	}
	if e.rng.Float64() < partialProb {
		minR := e.cfg.PartialFillMinRatio
		ratio := minR + e.rng.Float64()*(1.0-minR)
		partial := int(float64(fillQty) * ratio)
		if partial < 1 {
			partial = 1
		}
		if partial < fillQty {
			log.Printf("  📦 [Realistic] %-8s partial fill: %d→%d (%.0f%%, highVol=%v)",
				order.Symbol, fillQty, partial, ratio*100, highVol)
			fillQty = partial
		}
	}

	// ── 6. Feature 3: Market impact slippage ──────────────────────────────
	// impact ∝ sqrt(order_value / market_turnover)
	impactPct := 0.0
	if quote.Volume > 0 && quote.Price > 0 && e.cfg.MarketImpactCoeff > 0 {
		orderValue := delayedPrice * float64(fillQty)
		marketTurnover := quote.Price * float64(quote.Volume)
		participationRate := orderValue / marketTurnover
		impactPct = e.cfg.MarketImpactCoeff * math.Sqrt(participationRate)
	}

	// ── 7. Assemble total friction & fill price ────────────────────────────
	baseSlip := e.cfg.SlippageBasePct +
		e.rng.Float64()*e.cfg.SlippageRandPct +
		e.rng.Float64()*e.cfg.DelaySlippagePct

	var slippageAdjustedPrice float64
	switch order.Side {
	case "BUY":
		slippageAdjustedPrice = delayedPrice * (1 + baseSlip + impactPct)
	case "SELL":
		slippageAdjustedPrice = delayedPrice * (1 - baseSlip - impactPct)
	default:
		return nil, fmt.Errorf("realistic: unknown order side %q", order.Side)
	}

	grossAmount := slippageAdjustedPrice * float64(fillQty)
	_, _, _, totalFee, err := calculateTradingFee(order.Side, grossAmount, e.cfg)
	if err != nil {
		return nil, err
	}
	feePerShare := totalFee / float64(fillQty)

	fillPrice := slippageAdjustedPrice
	switch order.Side {
	case "BUY":
		fillPrice += feePerShare
	case "SELL":
		fillPrice -= feePerShare
		if fillPrice <= 0 {
			fillPrice = 0.001
		}
	}

	return &core.Trade{
		Symbol:    order.Symbol,
		Side:      order.Side,
		Price:     fillPrice,
		Quantity:  fillQty,
		Reason:    order.Reason,
		Timestamp: time.Now().UnixMilli() + int64(delayMs),
	}, nil
}

type tradingCostFile struct {
	CommissionPct  float64 `json:"commission_pct"`
	StampTaxPct    float64 `json:"stamp_tax_pct"`
	TransferFeePct float64 `json:"transfer_fee_pct"`
	MinCommission  float64 `json:"min_commission"`
}

// LoadTradingCostConfig reads trading-cost settings from path and overlays only cost fields.
func LoadTradingCostConfig(path string, cfg Config) (Config, error) {
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}

	var cost tradingCostFile
	if err := json.Unmarshal(data, &cost); err != nil {
		return cfg, err
	}

	if cost.CommissionPct < 0 || cost.StampTaxPct < 0 || cost.TransferFeePct < 0 || cost.MinCommission < 0 {
		return cfg, fmt.Errorf("realistic: trading cost config contains negative value")
	}
	if cost.CommissionPct > 0 {
		cfg.CommissionPct = cost.CommissionPct
	}
	if cost.StampTaxPct > 0 {
		cfg.StampTaxPct = cost.StampTaxPct
	}
	if cost.TransferFeePct > 0 {
		cfg.TransferFeePct = cost.TransferFeePct
	}
	if cost.MinCommission > 0 {
		cfg.MinCommission = cost.MinCommission
	}

	return cfg, nil
}

func calculateTradingFee(side string, amount float64, cfg Config) (commission float64, stampTax float64, transferFee float64, totalFee float64, err error) {
	if amount <= 0 {
		return 0, 0, 0, 0, nil
	}

	switch side {
	case "BUY", "SELL":
		if cfg.CommissionPct > 0 {
			commission = amount * cfg.CommissionPct
			if commission < cfg.MinCommission {
				commission = cfg.MinCommission
			}
		}
		if side == "SELL" {
			stampTax = amount * cfg.StampTaxPct
		}
		transferFee = amount * cfg.TransferFeePct
	default:
		return 0, 0, 0, 0, fmt.Errorf("realistic: unknown order side %q", side)
	}

	totalFee = commission + stampTax + transferFee
	return commission, stampTax, transferFee, totalFee, nil
}

// LastDelayMs returns the simulated execution delay of the most recent Execute call (ms).
func (e *Executor) LastDelayMs() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastDelayMs
}

// CostSummary returns the effective round-trip cost percentage (for reporting).
func (e *Executor) CostSummary() string {
	baseSlip := (e.cfg.SlippageBasePct + e.cfg.DelaySlippagePct) * 100
	return fmt.Sprintf(
		"手续费%.4f%%(最低%.2f元)  印花税卖出%.4f%%  过户费%.4f%%  基础滑点%.2f%%"+
			"  +市场冲击sqrt(coeff=%.2f)  延迟%d-%dms",
		e.cfg.CommissionPct*100,
		e.cfg.MinCommission,
		e.cfg.StampTaxPct*100,
		e.cfg.TransferFeePct*100,
		baseSlip,
		e.cfg.MarketImpactCoeff,
		int(e.cfg.MinDelayMs), int(e.cfg.MaxDelayMs),
	)
}
