# Skill: trader_main

## 1) Header
- **服务名称**: `trader_main`（根入口 `main.go`）
- **核心语言**: Go 1.25
- **主要依赖**: `astock_trade/engine`, `astock_trade/alpha/registry`, `astock_trade/provider/mock`, `astock_trade/executor/simulated`, `astock_trade/adaptive`

## 2) Code Fingerprints (代码指纹)
### 代表性函数签名（3个）
1. `func main()`
2. `func New(cfg Config, entries ...Entry) *Registry`（`alpha/registry`）
3. `func New(cfg Config, ...deps) *Engine`（`engine`）

### 参数命名规范
- Go 风格：导出类型/字段用 `PascalCase`，局部变量/参数用 `camelCase`。
- 配置参数高频前缀：`Max*`, `Min*`, `*Threshold`, `*Pct`, `Tick*`。
- 跨模块统一语义参数：`symbols`, `quotes`, `positions`, `equity`, `tick`.

## 3) Context Anchors (上下文锚点)
### 全局常量
- `demoRunDuration`, `tradeLogPath`, `indexSymbol`（根 `main.go`）

### 异常/错误锚点
- 根入口主要采用 `log.Fatalf` 失败即终止，错误不吞并。
- 核心执行错误由下游模块返回 `error`，入口侧统一兜底退出。

### 必须引用的内部模块
- `engine`, `portfolio`, `position`, `decision/topn`
- `alpha/*` + `alpha/registry`
- `signal/dampener`, `signal/stability`, `market/trend`

## 4) Implementation SOP (实现标准)
### 异步要求
- 该服务主流程为**同步 tick 驱动**；新增后台任务必须由 `context.Context` 控制生命周期。
- 不允许在策略路径引入无界 goroutine。

### 安全要求
- 新增交易决策必须经过现有 `marketFilter + decision + position/portfolio` 链路，不可绕过。
- 风控阈值变更必须以 `Config` 注入，禁止硬编码在业务分支。

### 性能要求
- Tick 内计算保持 O(股票池规模)；禁止每 tick 做阻塞 I/O。
- 复用已有缓存/状态（例如 rolling 指标），避免重复扫描历史数据。

## 5) Codex Scaffolding (注释模板)
```go
// <service:trader_main>
// <intent>实现/修改主模拟交易编排逻辑</intent>
// <constraints>
// 1) 不改变 engine.New 依赖注入顺序与语义
// 2) 不绕过 marketFilter/position/portfolio 风控链
// 3) 新增后台逻辑必须可被 context 取消
// </constraints>
// <risk-check>验证 tick 路径无阻塞 I/O，参数阈值走 Config</risk-check>
```

