package dashboard

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"astock_trade/broker/paper"
	"astock_trade/core"
	"astock_trade/monitor"
	"astock_trade/risk"
	"astock_trade/safety"
	"astock_trade/store"
)

const equitySpikeThreshold = 0.20

type client struct {
	conn *websocket.Conn
	send chan []byte
}

type Server struct {
	addr string

	mon         *monitor.Monitor
	safetyGuard *safety.Guard
	riskEngine  *risk.Engine
	posMgr      core.PositionManager
	perfTracker core.PerformanceTracker
	broker      *paper.Broker
	alphaEng    core.AlphaEngine

	upgrader   websocket.Upgrader
	register   chan *client
	unregister chan *client
	broadcast  chan []byte
	clients    map[*client]struct{}

	mu          sync.RWMutex
	equityCurve []EquityPoint
	peakEquity  float64
	todayOpen   float64
	alerts      []AlertInfo
	lastMarket  MarketInfo
	lastTick    int
	watchCounts map[string]int
	lastSignals map[string]core.Signal
	lastSnap    Snapshot

	equityMaxLen int
	alertMaxLen  int
	tradeMaxLen  int
	staticDir    string

	dbWriter *store.Writer
	dbStore  *store.Store
}

type Config struct {
	Addr         string
	StaticDir    string
	EquityMaxLen int
	AlertMaxLen  int
	TradeMaxLen  int
}

func New(cfg Config, mon *monitor.Monitor, sg *safety.Guard, re *risk.Engine,
	posMgr core.PositionManager, perf core.PerformanceTracker,
	broker *paper.Broker, alpha core.AlphaEngine,
) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":18099"
	}
	if cfg.EquityMaxLen <= 0 {
		cfg.EquityMaxLen = 600
	}
	if cfg.AlertMaxLen <= 0 {
		cfg.AlertMaxLen = 30
	}
	if cfg.TradeMaxLen <= 0 {
		cfg.TradeMaxLen = 50
	}
	s := &Server{
		addr:         cfg.Addr,
		staticDir:    cfg.StaticDir,
		mon:          mon,
		safetyGuard:  sg,
		riskEngine:   re,
		posMgr:       posMgr,
		perfTracker:  perf,
		broker:       broker,
		alphaEng:     alpha,
		register:     make(chan *client, 8),
		unregister:   make(chan *client, 8),
		broadcast:    make(chan []byte, 8),
		clients:      make(map[*client]struct{}),
		equityMaxLen: cfg.EquityMaxLen,
		alertMaxLen:  cfg.AlertMaxLen,
		tradeMaxLen:  cfg.TradeMaxLen,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
	}
	if mon != nil {
		mon.OnAlert(func(evt core.AlertEvent) {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.alerts = append(s.alerts, AlertInfo{
				Level:     evt.Level.String(),
				Message:   evt.Message,
				Timestamp: evt.Timestamp,
				Drawdown:  evt.Drawdown,
			})
			if len(s.alerts) > s.alertMaxLen {
				s.alerts = s.alerts[len(s.alerts)-s.alertMaxLen:]
			}
		})
	}
	return s
}

var _ core.DashboardReporter = (*Server)(nil)

func (s *Server) OnTick(equity float64, report core.PerformanceReport, positions []core.Position, quotes map[string]*core.Quote) {
	s.mu.Lock()
	s.lastTick++
	tick := s.lastTick
	if equity > s.peakEquity {
		s.peakEquity = equity
	}
	if s.todayOpen == 0 {
		s.todayOpen = equity
	}
	dd := 0.0
	if s.peakEquity > 0 {
		dd = (s.peakEquity - equity) / s.peakEquity * 100
	}
	s.equityCurve = append(s.equityCurve, EquityPoint{Tick: tick, Equity: equity, Drawdown: dd})
	if len(s.equityCurve) > s.equityMaxLen {
		s.equityCurve = s.equityCurve[len(s.equityCurve)-s.equityMaxLen:]
	}
	equityCopy := append([]EquityPoint(nil), s.equityCurve...)
	alertsCopy := append([]AlertInfo(nil), s.alerts...)
	todayOpen := s.todayOpen
	s.mu.Unlock()

	snap := Snapshot{
		Timestamp:       time.Now().UnixMilli(),
		Equity:          equityCopy,
		Alerts:          alertsCopy,
		Account:         s.buildAccount(equity, report, positions, quotes, todayOpen),
		Positions:       s.buildPositions(positions, quotes, report),
		PositionHistory: s.buildPositionHistory(),
		Trades:          s.buildTrades(),
		Safety:          s.buildSafety(),
		Risk:            s.buildRisk(),
		Execution:       s.buildExecution(),
		Strategies:      s.buildStrategies(),
		Market:          s.buildMarket(quotes),
		Candidates:      s.buildCandidates(positions, quotes),
	}
	s.mu.Lock()
	s.lastSnap = snap
	s.mu.Unlock()
	s.pushSnapshot(snap)

	if s.dbWriter != nil {
		now := snap.Timestamp
		s.dbWriter.WriteEquityPoint(store.EquityRow{Timestamp: now, Equity: equity, Drawdown: dd, Cash: snap.Account.Cash, PositionValue: snap.Account.InvestedValue})
		posRows := make([]store.PosRow, 0, len(positions))
		for _, p := range positions {
			cur := currentPrice(p, quotes)
			pnl := 0.0
			if p.AvgPrice > 0 {
				pnl = (cur - p.AvgPrice) / p.AvgPrice * 100
			}
			posRows = append(posRows, store.PosRow{Symbol: p.Symbol, Qty: p.Quantity, AvgPrice: p.AvgPrice, MarketValue: cur * float64(p.Quantity), UnrealizedPnl: pnl, UpdatedAt: now})
		}
		s.dbWriter.SyncPositions(posRows)
		if s.safetyGuard != nil && s.mon != nil {
			st := s.safetyGuard.SafetyStatus()
			monSt := s.mon.State()
			s.dbWriter.WriteSystemStatus(store.StatusRow{Timestamp: now, Streak: st.CurrentStreak, RiskLevel: monSt.RiskLevel.String(), MaxPositionPct: st.StreakScale * 0.80, AllowOpen: s.safetyGuard.AllowOpen(), KillSwitchActive: monSt.RiskLevel == core.RiskEmergency, AnomalyCount: st.AbnormalCount})
		}
	}
}

func (s *Server) OnQuoteRefresh(equity float64, report core.PerformanceReport, positions []core.Position, quotes map[string]*core.Quote) {
	s.mu.RLock()
	todayOpen := s.todayOpen
	if todayOpen == 0 {
		todayOpen = equity
	}
	equityCopy := append([]EquityPoint(nil), s.equityCurve...)
	alertsCopy := append([]AlertInfo(nil), s.alerts...)
	lastSnap := s.lastSnap
	s.mu.RUnlock()

	positionHistory, trades := lastSnap.PositionHistory, lastSnap.Trades
	safetyInfo, riskInfo := lastSnap.Safety, lastSnap.Risk
	execution, strategies := lastSnap.Execution, lastSnap.Strategies
	marketInfo := s.buildMarket(quotes)
	candidates := lastSnap.Candidates
	if lastSnap.Timestamp == 0 {
		positionHistory = s.buildPositionHistory()
		trades = s.buildTrades()
		safetyInfo = s.buildSafety()
		riskInfo = s.buildRisk()
		execution = s.buildExecution()
		strategies = s.buildStrategies()
		candidates = s.buildCandidates(positions, quotes)
	}

	snap := Snapshot{
		Timestamp:       time.Now().UnixMilli(),
		Equity:          equityCopy,
		Alerts:          alertsCopy,
		Account:         s.buildAccount(equity, report, positions, quotes, todayOpen),
		Positions:       s.buildPositions(positions, quotes, report),
		PositionHistory: positionHistory,
		Trades:          trades,
		Safety:          safetyInfo,
		Risk:            riskInfo,
		Execution:       execution,
		Strategies:      strategies,
		Market:          marketInfo,
		Candidates:      candidates,
	}
	s.mu.Lock()
	s.lastSnap = snap
	s.mu.Unlock()
	s.pushSnapshot(snap)
}

func (s *Server) SetMarketState(state string, indexPrice float64) {
	s.mu.Lock()
	s.lastMarket = MarketInfo{State: state, IndexPrice: indexPrice}
	s.mu.Unlock()
}

func (s *Server) SetWatchList(counts map[string]int) {
	s.mu.Lock()
	s.watchCounts = counts
	s.mu.Unlock()
}

func (s *Server) SetSignalCache(signals []core.Signal) {
	m := make(map[string]core.Signal, len(signals))
	for _, sig := range signals {
		m[sig.Symbol] = sig
	}
	s.mu.Lock()
	s.lastSignals = m
	s.mu.Unlock()
}

func (s *Server) SetWriter(w *store.Writer) { s.dbWriter = w }

func (s *Server) SetStore(st *store.Store) {
	s.dbStore = st
	s.bootstrapFromStore()
}

func (s *Server) ListenAndServe() error {
	go s.hub()
	s.primeSnapshot()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if s.staticDir != "" {
		if _, err := os.Stat(s.staticDir); err == nil {
			mux.Handle("/", http.FileServer(http.Dir(s.staticDir)))
			log.Printf("[Dashboard] 前端访问地址:  http://localhost%s", s.addr)
		} else {
			log.Printf("[Dashboard] 警告: StaticDir %q 不存在, 跳过静态文件托管", s.staticDir)
		}
	}
	if s.dbStore != nil {
		mux.HandleFunc("/api/equity", s.handleAPIEquity)
		mux.HandleFunc("/api/executions", s.handleAPIExecutions)
		mux.HandleFunc("/api/positions", s.handleAPIPositions)
		mux.HandleFunc("/api/risk-events", s.handleAPIRiskEvents)
		mux.HandleFunc("/api/system-status/latest", s.handleAPISystemStatus)
	}
	log.Printf("[Dashboard] WebSocket 服务启动 ws://localhost%s/ws", s.addr)
	return http.ListenAndServe(s.addr, mux)
}

func (s *Server) primeSnapshot() {
	if s.posMgr == nil || s.perfTracker == nil {
		return
	}
	positions := s.posMgr.AllPositions()
	report := s.perfTracker.Report()
	equity := report.CurrentEquity
	if equity <= 0 {
		equity = s.perfTracker.Cash()
		for _, p := range positions {
			equity += p.AvgPrice * float64(p.Quantity)
		}
	}
	s.OnQuoteRefresh(equity, report, positions, map[string]*core.Quote{})
}

func (s *Server) hub() {
	var lastSnapshot []byte
	for {
		select {
		case c := <-s.register:
			s.clients[c] = struct{}{}
			if lastSnapshot != nil {
				select { case c.send <- lastSnapshot: default: }
			}
		case c := <-s.unregister:
			if _, ok := s.clients[c]; ok {
				delete(s.clients, c)
				close(c.send)
			}
		case msg := <-s.broadcast:
			lastSnapshot = msg
			for c := range s.clients {
				select {
				case c.send <- msg:
				default:
					close(c.send)
					delete(s.clients, c)
				}
			}
		}
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Dashboard] upgrade error: %v", err)
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 32)}
	s.register <- c
	go s.writePump(c)
	s.readPump(c)
}

func (s *Server) writePump(c *client) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() { ticker.Stop(); _ = c.conn.Close() }()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) readPump(c *client) {
	defer func() { s.unregister <- c; _ = c.conn.Close() }()
	c.conn.SetReadLimit(512)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var cmd Command
		if err := json.Unmarshal(msg, &cmd); err == nil && cmd.Type == "command" {
			s.handleCommand(cmd)
		}
	}
}

func (s *Server) handleCommand(cmd Command) {
	if s.safetyGuard == nil {
		return
	}
	switch cmd.Action {
	case "stop_opening":
		s.safetyGuard.StopOpening()
	case "resume_opening":
		s.safetyGuard.ResumeOpening()
	case "force_liquidate":
		s.safetyGuard.TriggerForceLiquidate()
	default:
		log.Printf("[Dashboard] 未知指令: %q", cmd.Action)
	}
	s.pushCommandSnapshot()
}

func (s *Server) pushCommandSnapshot() {
	s.mu.RLock()
	snap := s.lastSnap
	s.mu.RUnlock()
	if snap.Timestamp == 0 {
		return
	}
	snap.Timestamp = time.Now().UnixMilli()
	snap.Safety = s.buildSafety()
	s.pushSnapshot(snap)
}

func (s *Server) pushSnapshot(snap Snapshot) {
	data, err := json.Marshal(snap)
	if err != nil {
		log.Printf("[Dashboard] marshal error: %v", err)
		return
	}
	select {
	case s.broadcast <- data:
	default:
	}
}

func (s *Server) buildAccount(equity float64, report core.PerformanceReport, positions []core.Position, quotes map[string]*core.Quote, todayOpen float64) AccountInfo {
	cash := 0.0
	if s.perfTracker != nil {
		cash = s.perfTracker.Cash()
	}
	invested := 0.0
	for _, p := range positions {
		invested += currentPrice(p, quotes) * float64(p.Quantity)
	}
	posPct, todayRet := 0.0, 0.0
	if equity > 0 {
		posPct = invested / equity * 100
	}
	if todayOpen > 0 {
		todayRet = (equity - todayOpen) / todayOpen * 100
	}
	initialCapital := report.InitialCapital
	totalReturn := report.TotalReturn
	riskLevel := "NORMAL"
	curDD := 0.0
	if s.mon != nil {
		st := s.mon.State()
		riskLevel = st.RiskLevel.String()
		curDD = st.DrawdownPct
	}
	if initialCapital > 0 {
		totalReturn = (equity - initialCapital) / initialCapital * 100
		curDD = 0
		if equity < initialCapital {
			curDD = (initialCapital - equity) / initialCapital * 100
		}
	}
	return AccountInfo{TotalEquity: equity, InitialCapital: initialCapital, Cash: cash, InvestedValue: invested, TodayReturnPct: todayRet, TotalReturnPct: totalReturn, CurrentDrawdownPct: curDD, MaxDrawdownPct: report.MaxDrawdown, PositionPct: posPct, RiskLevel: riskLevel, TickCount: report.TickCount, WinRate: report.WinRate, TradeCount: report.TradeCount, ProfitFactor: report.ProfitFactor}
}

func (s *Server) buildPositions(positions []core.Position, quotes map[string]*core.Quote, _ core.PerformanceReport) []PositionInfo {
	out := make([]PositionInfo, 0, len(positions))
	defense := false
	if s.mon != nil {
		defense = s.mon.State().RiskLevel >= core.RiskDefense
	}
	for _, p := range positions {
		cur := currentPrice(p, quotes)
		cost := p.AvgPrice * float64(p.Quantity)
		pnlPct := 0.0
		if p.AvgPrice > 0 {
			pnlPct = (cur - p.AvgPrice) / p.AvgPrice * 100
		}
		out = append(out, PositionInfo{Symbol: p.Symbol, Quantity: p.Quantity, AvgPrice: p.AvgPrice, CurrentPrice: cur, Cost: cost, MarketValue: cur * float64(p.Quantity), PnlPct: pnlPct, PnlAbs: (cur - p.AvgPrice) * float64(p.Quantity), DefenseFlag: defense})
	}
	return out
}

func (s *Server) buildTrades() []TradeInfo {
	if s.broker == nil {
		return nil
	}
	recs := s.broker.Records()
	start := 0
	if len(recs) > s.tradeMaxLen {
		start = len(recs) - s.tradeMaxLen
	}
	out := make([]TradeInfo, 0, len(recs)-start)
	for i := len(recs) - 1; i >= start; i-- {
		r := recs[i]
		out = append(out, TradeInfo{OrderID: r.OrderID, Symbol: r.Symbol, Side: r.Side, TheoPrice: r.TheoreticalPrice, FillPrice: r.ActualPrice, SlippagePct: r.SlippagePct, Qty: r.OrderQty, FilledQty: r.FilledQty, FillRate: r.FillRate, Status: r.Status, LatencyMs: r.Latency, Timestamp: r.ExecutionTime, Reason: r.Reason})
	}
	return out
}

func (s *Server) buildPositionHistory() []ClosedTradeInfo {
	if s.perfTracker == nil {
		return nil
	}
	closed := s.perfTracker.ClosedTrades()
	out := make([]ClosedTradeInfo, 0, len(closed))
	for i := len(closed) - 1; i >= 0; i-- {
		c := closed[i]
		out = append(out, ClosedTradeInfo{Symbol: c.Symbol, EntryPrice: c.EntryPrice, ExitPrice: c.ExitPrice, Quantity: c.Quantity, PnlPct: c.PnlPct, PnlAbs: (c.ExitPrice - c.EntryPrice) * float64(c.Quantity), HoldTicks: c.HoldTicks, ExitReason: exitReasonCN(c.ExitReason), Timestamp: c.Timestamp})
	}
	return out
}

func (s *Server) buildSafety() SafetyInfo {
	if s.safetyGuard == nil {
		return SafetyInfo{}
	}
	st := s.safetyGuard.SafetyStatus()
	return SafetyInfo{Streak: st.CurrentStreak, FreezeLeft: st.FreezeTicksLeft, StreakScale: st.StreakScale, ManualStopOpen: st.ManualStopOpen, ForceLiqPending: st.ForceLiqPending, AbnormalCount: st.AbnormalCount, TradingStopped: st.TradingStopped, AllowOpen: s.safetyGuard.AllowOpen()}
}

func (s *Server) buildRisk() RiskInfo {
	if s.riskEngine != nil {
		st := s.riskEngine.CurrentState()
		return RiskInfo{Tier: st.Tier.String(), DrawdownPct: st.DrawdownPct, VolPct: st.VolatilityPct, DDScale: st.DDScale, VolScale: st.VolScale, EffectivePct: st.EffectivePct, IsFrozen: st.IsFrozen, FreezeLeft: st.FreezeTicksLeft}
	}
	if s.mon != nil {
		st := s.mon.State()
		return RiskInfo{Tier: st.RiskLevel.String(), DrawdownPct: st.DrawdownPct}
	}
	return RiskInfo{}
}

func (s *Server) buildExecution() ExecInfo {
	if s.broker == nil {
		return ExecInfo{}
	}
	recs := s.broker.Records()
	if len(recs) == 0 {
		return ExecInfo{}
	}
	filled, rejected := 0, 0
	slips := make([]float64, 0, len(recs))
	lats := make([]float64, 0, len(recs))
	sumSlip, sumLat := 0.0, 0.0
	for _, r := range recs {
		if r.Status == "FILLED" {
			filled++
		}
		if r.Status == "REJECTED" {
			rejected++
		}
		slip := r.SlippagePct
		lat := float64(r.Latency)
		slips = append(slips, slip)
		lats = append(lats, lat)
		sumSlip += slip
		sumLat += lat
	}
	n := float64(len(recs))
	return ExecInfo{TotalOrders: len(recs), FillRate: float64(filled) / n * 100, RejectionRate: float64(rejected) / n * 100, AvgSlippagePct: sumSlip / n, P50SlippagePct: percentile(slips, 0.50), P90SlippagePct: percentile(slips, 0.90), AvgLatencyMs: sumLat / n, P90LatencyMs: percentile(lats, 0.90)}
}

func (s *Server) buildStrategies() []StrategyInfo {
	reg, ok := s.alphaEng.(core.StrategyRegistry)
	if !ok {
		return nil
	}
	weights := reg.WeightSnapshot()
	out := make([]StrategyInfo, 0, len(weights))
	for _, w := range weights {
		out = append(out, StrategyInfo{Name: w.Name, Weight: w.Weight, BaseWeight: w.BaseWeight, WinRate: w.WinRate, AvgPnl: w.AvgPnL, Trades: w.TradeCount})
	}
	return out
}

func (s *Server) buildMarket(quotes map[string]*core.Quote) MarketInfo {
	s.mu.RLock()
	m := s.lastMarket
	s.mu.RUnlock()
	for _, sym := range []string{"000001", "000001.SH", "SH000001", "000300", "000300.SH", "399300", "399300.SZ", "CSI300"} {
		if q, ok := quotes[sym]; ok && q.Price > 0 {
			m.IndexPrice = q.Price
			break
		}
	}
	return m
}

func (s *Server) buildCandidates(positions []core.Position, quotes map[string]*core.Quote) []CandidateInfo {
	s.mu.RLock()
	signals := make(map[string]core.Signal, len(s.lastSignals))
	for k, v := range s.lastSignals { signals[k] = v }
	counts := make(map[string]int, len(s.watchCounts))
	for k, v := range s.watchCounts { counts[k] = v }
	s.mu.RUnlock()
	if len(signals) == 0 {
		return nil
	}
	inPos := make(map[string]bool, len(positions))
	for _, p := range positions { inPos[p.Symbol] = true }
	if s.dbStore != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		rankings, err := s.dbStore.GetTopRankings(ctx, 50)
		cancel()
		if err != nil {
			log.Printf("[Dashboard] 读取候选池基础数据失败: %v", err)
		} else if len(rankings) > 0 {
			out := make([]CandidateInfo, 0, len(rankings))
			for i, row := range rankings {
				rank := row.Rank
				if rank <= 0 {
					rank = i + 1
				}
				price, pct := row.Price, 0.0
				if q, ok := quotes[row.Symbol]; ok && q != nil {
					price, pct = q.Price, q.PctChg
				}
				info := CandidateInfo{Rank: rank, Symbol: row.Symbol, Name: row.Name, Score: row.Score, LiveScore: row.Score, Price: price, PctChg: pct, Ret5d: row.Ret5d, VolumeRatio: row.VolumeRatio, MktCapB: row.MktCap / 1e8, Stability: counts[row.Symbol], InPosition: inPos[row.Symbol]}
				if sig, ok := signals[row.Symbol]; ok {
					info.LiveScore = sig.Score
					info.Breakdown = sig.Breakdown
				}
				out = append(out, info)
			}
			return out
		}
	}
	out := make([]CandidateInfo, 0, len(signals))
	rank := 1
	for sym, sig := range signals {
		price, pct := 0.0, 0.0
		if q, ok := quotes[sym]; ok && q != nil {
			price, pct = q.Price, q.PctChg
		}
		out = append(out, CandidateInfo{Rank: rank, Symbol: sym, Score: sig.Score, LiveScore: sig.Score, Breakdown: sig.Breakdown, Price: price, PctChg: pct, Stability: counts[sym], InPosition: inPos[sym]})
		rank++
	}
	return out
}

func currentPrice(p core.Position, quotes map[string]*core.Quote) float64 {
	if q, ok := quotes[p.Symbol]; ok && q != nil && q.Price > 0 {
		return q.Price
	}
	return p.AvgPrice
}

func exitReasonCN(reason string) string {
	switch reason {
	case "STOP_LOSS":
		return "止损"
	case "TAKE_PROFIT":
		return "止盈"
	case "TRAIL_STOP":
		return "追踪止盈"
	case "ROTATION":
		return "轮动调仓"
	default:
		return "卖出"
	}
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	v := append([]float64(nil), values...)
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
	idx := int(math.Ceil(p*float64(len(v)))) - 1
	if idx < 0 { idx = 0 }
	if idx >= len(v) { idx = len(v)-1 }
	return v[idx]
}

func (s *Server) bootstrapFromStore() {
	if s.dbStore == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	equityRows, err := s.dbStore.QueryEquityCurve(ctx, "all")
	if err != nil {
		log.Printf("[Dashboard] 启动回填权益曲线失败: %v", err)
	} else if len(equityRows) > 0 {
		equityRows = sanitizeEquityRows(equityRows)
		start := 0
		if len(equityRows) > s.equityMaxLen {
			start = len(equityRows) - s.equityMaxLen
		}
		points := make([]EquityPoint, 0, len(equityRows)-start)
		peak := 0.0
		for idx, row := range equityRows[start:] {
			points = append(points, EquityPoint{Tick: idx + 1, Equity: row.Equity, Drawdown: row.Drawdown})
			if row.Equity > peak { peak = row.Equity }
		}
		s.mu.Lock()
		s.equityCurve = points
		s.peakEquity = peak
		s.todayOpen = equityRows[len(equityRows)-1].Equity
		s.lastTick = len(points)
		s.mu.Unlock()
	}
	alertRows, err := s.dbStore.QueryRiskEvents(ctx, s.alertMaxLen)
	if err != nil {
		log.Printf("[Dashboard] 启动回填告警失败: %v", err)
		return
	}
	alerts := make([]AlertInfo, 0, len(alertRows))
	for i := len(alertRows) - 1; i >= 0; i-- {
		row := alertRows[i]
		alerts = append(alerts, AlertInfo{Level: row.EventType, Message: row.Description, Timestamp: row.Timestamp, Drawdown: row.Drawdown})
	}
	s.mu.Lock()
	s.alerts = alerts
	s.mu.Unlock()
}

func sanitizeEquityRows(rows []store.EquityQueryRow) []store.EquityQueryRow {
	if len(rows) < 3 {
		return append([]store.EquityQueryRow(nil), rows...)
	}
	out := make([]store.EquityQueryRow, 0, len(rows))
	for i, row := range rows {
		if i > 0 && i < len(rows)-1 && isIsolatedEquitySpike(rows[i-1].Equity, row.Equity, rows[i+1].Equity) {
			log.Printf("[Dashboard] 忽略异常权益点 ts=%d equity=%.2f", row.Timestamp, row.Equity)
			continue
		}
		out = append(out, row)
	}
	return out
}

func isIsolatedEquitySpike(prev, cur, next float64) bool {
	if prev <= 0 || cur <= 0 || next <= 0 {
		return false
	}
	return math.Abs(cur-prev)/prev >= equitySpikeThreshold && math.Abs(cur-next)/cur >= equitySpikeThreshold && math.Abs(next-prev)/prev <= 0.05
}

func apiJSON(w http.ResponseWriter, _ *http.Request, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleAPIEquity(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if rng == "" { rng = "all" }
	rows, err := s.dbStore.QueryEquityCurve(r.Context(), rng)
	apiJSON(w, r, rows, err)
}

func (s *Server) handleAPIExecutions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.dbStore.QueryExecutions(r.Context(), r.URL.Query().Get("symbol"), limit)
	apiJSON(w, r, rows, err)
}

func (s *Server) handleAPIPositions(w http.ResponseWriter, r *http.Request) {
	rows, err := s.dbStore.QueryPositions(r.Context())
	apiJSON(w, r, rows, err)
}

func (s *Server) handleAPIRiskEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	rows, err := s.dbStore.QueryRiskEvents(r.Context(), limit)
	apiJSON(w, r, rows, err)
}

func (s *Server) handleAPISystemStatus(w http.ResponseWriter, r *http.Request) {
	row, err := s.dbStore.QueryLatestSystemStatus(r.Context())
	if row == nil && err == nil {
		apiJSON(w, r, map[string]any{}, nil)
		return
	}
	apiJSON(w, r, row, err)
}
