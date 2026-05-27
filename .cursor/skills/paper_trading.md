# Skill: paper_trading

## 1) Header
- **服务名称**: `paper_trading`（`cmd/paper`）
- **核心语言**: Go
- **主要依赖**: `provider/eastmoney`, `provider/replay`, `broker/paper`, `executor/realistic`, `safety`, `monitor`, `risk`, `dashboard`, `store`

## 2) Code Fingerprints (代码指纹)
### 代表性函数签名（3个）
1. `func main()`
2. `func restoreRuntimeState(execPath string, initialCash float64, lookback time.Duration) (*restoredRuntimeState, error)`
3. `func runAlphaScheduler(ctx context.Context, st *store.Store, sc *dynamic.Screener, dp core.DataProvider)`

### 参数命名规范
- 延续 Go `camelCase`，配置常量统一 `snake_like` 常量名少见，主流是 `drawdownCautionPct` 这类语义化 camelCase。
- 运行态参数常见：`ctx`, `runCtx`, `cancel`, `dbStore`, `dbWriter`, `safetyGuard`。
- 环境变量统一 `ASTOCK_*` 前缀。

## 3) Context Anchors (上下文锚点)
### 全局常量
- `paperRunDuration`, `tradeLogPath`, `execLogPath`, `positionStatePath`, `dashboardAddr`
- 回撤阈值：`drawdownCautionPct`, `drawdownDefensePct`, `drawdownTier3Pct`, `drawdownEmergencyPct`

### 异常/错误锚点
- 数据/日志/状态恢复失败按严重度分层：关键路径 `log.Fatalf`，可降级路径记录 warning 继续运行。
- DB 写入幂等依赖 `store/writer` + SQL `ON CONFLICT`。

### 必须引用的内部模块
- `safety.New(...)`、`mon.SetSafetyGuard(...)`、`eng.SetSafetyGuard(...)`
- `paperBroker.SetLogger(...)` + `safetyGuard.CheckExecution(...)`
- `store.Open/Migrate/NewWriter.Start`（启用持久化时）

## 4) Implementation SOP (实现标准)
### 异步要求
- 所有后台任务（Dashboard、Scheduler、Report）必须 goroutine + `context` 可取消。
- 新增定时任务必须支持超时 (`context.WithTimeout`) 与重试边界。

### 安全要求
- 任何下单相关改动必须保留 `safety.Guard` 全链路：`AllowOpen/TriggerForceLiquidate/CheckExecution`。
- 不能绕开持仓快照持久化：退出前必须 `SaveState(positionStatePath)`。
- 人工控制信号 `SIGUSR1/SIGUSR2/SIGHUP` 行为不得破坏。

### 性能要求
- 实时行情侧必须复用连接与并发限流（eastmoney provider 已有并发信号量和缓存）。
- DB 写入走异步批量 writer，不可在 tick 热路径直写数据库。

## 5) Codex Scaffolding (注释模板)
```go
// <service:paper_trading>
// <intent>实现/修改实盘仿真主流程</intent>
// <constraints>
// 1) 下单前后必须经过 safety.Guard 与 monitor/risk
// 2) 不破坏持仓与执行日志恢复逻辑
// 3) 新增 goroutine 必须有 context 取消与超时
// 4) DB 写入仅通过 store.Writer，不在热路径直写
// </constraints>
// <risk-check>验证 kill switch、人工信号、状态保存仍可触发</risk-check>
```

