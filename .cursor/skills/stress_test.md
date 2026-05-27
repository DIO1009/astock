# Skill: stress_test

## 1) Header
- **服务名称**: `stress_test`（`cmd/stress`）
- **核心语言**: Go
- **主要依赖**: `provider/stress`, `executor/realistic`, `risk`, `portfolio/sector`, `engine`

## 2) Code Fingerprints (代码指纹)
### 代表性函数签名（3个）
1. `func main()`
2. `func New(cancelFn func()) *Provider`（`provider/stress`）
3. `func printStressReport(... ) string`（`cmd/stress`）

### 参数命名规范
- 强调 regime 语义：`regimeTracker`, `tradesByRegime`, `maxDDByRegime`。
- 指标命名统一 `%` 后缀语义：`pnlPct`, `WinRate`, `Drawdown`.

## 3) Context Anchors (上下文锚点)
### 全局常量
- `indexSym`, `logFile`, `totalCapital`
- 压测阶段常量在 `provider/stress.DefaultPhases()`

### 异常/错误锚点
- 压测报告应包含通过率，不能只输出原始日志。
- 风控状态通过 `risk.Engine` 暴露 `RiskState`，作为核心结果判据。

### 必须引用的内部模块
- `stressprov.New(cancelFn)`（1000 tick 场景推进）
- `risk.New(...)` + managed wrappers
- `executor/realistic`（摩擦与拒单）

## 4) Implementation SOP (实现标准)
### 异步要求
- 压测运行必须 `context.WithTimeout` 且场景结束自动 `cancel`。
- 新增统计器需保证并发安全（现有 tracker 用 `sync.Mutex`）。

### 安全要求
- 黑天鹅/高波动阶段不可被“平滑”掉；必须保留极端冲击。
- 风控触发标准（回撤、冻结、强平）不得弱化。

### 性能要求
- 场景数据应在 provider 内增量生成，不做全量历史回放重算。
- 报告聚合采用线性累计，避免重型排序热路径。

## 5) Codex Scaffolding (注释模板)
```go
// <service:stress_test>
// <intent>实现/修改压力测试与报告逻辑</intent>
// <constraints>
// 1) 必须保留多阶段市场与黑天鹅冲击
// 2) 风控状态输出需包含 tier/freeze/liquidate 信号
// 3) 新增统计结构必须并发安全
// </constraints>
// <risk-check>输出通过率、分 regime 指标、风险引擎状态</risk-check>
```

