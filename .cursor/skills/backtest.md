# Skill: backtest

## 1) Header
- **服务名称**: `backtest`（`cmd/backtest`）
- **核心语言**: Go
- **主要依赖**: `provider/replay`, `executor/realistic`, `analysis/deviation`, `broker/paper`, `engine`

## 2) Code Fingerprints (代码指纹)
### 代表性函数签名（3个）
1. `func main()`
2. `func generateBacktestCSV(path string, symbols []string, nBars int, seed int64)`
3. `func (e *Executor) Execute(order *core.Order, quote *core.Quote) (*core.Trade, error)`（`executor/realistic`）

### 参数命名规范
- 统一 `camelCase`，回测参数以 `tick*`, `tradingDays`, `barsPerDay`, `initialCapital` 命名。
- 统计类变量简短直观：`total`, `filled`, `partial`, `rejected`, `fillRate`。

## 3) Context Anchors (上下文锚点)
### 全局常量
- `tickInterval`, `tradingDays`, `barsPerDay`, `backtestSeed`, `csvDataPath`

### 异常/错误锚点
- 数据加载、执行日志初始化、引擎运行异常均为硬失败。
- 回测超时由 `context.WithTimeout` 控制，`DeadlineExceeded` 允许按正常结束处理。

### 必须引用的内部模块
- `replay.Provider.LoadCSV`
- `realistic.Default/New`（成本+拒单+冲击模型）
- `deviation.New().AddAll(...).PrintReport()`

## 4) Implementation SOP (实现标准)
### 异步要求
- 主流程保持可复现实验，不新增不确定并发写行为。
- 运行时长必须由 tick 推导超时，避免无限回测。

### 安全要求
- 回测撮合逻辑必须继续遵守涨跌停/拒单/成交量约束，不可简化为理想成交。
- 统计输出必须显式披露成交率和滑点，不得只报收益。

### 性能要求
- 历史数据加载一次，tick 内不重复解析 CSV。
- 报告计算应线性扫描，不做高复杂度聚合。

## 5) Codex Scaffolding (注释模板)
```go
// <service:backtest>
// <intent>实现/修改长周期回测逻辑</intent>
// <constraints>
// 1) 保持可复现性（seed + 固定数据生成规则）
// 2) 不能绕过 realistic executor 成本/拒单模型
// 3) 引擎运行必须有 context 超时保护
// </constraints>
// <risk-check>输出成交率/滑点/拒单率并校验阈值</risk-check>
```

