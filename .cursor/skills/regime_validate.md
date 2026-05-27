# Skill: regime_validate

## 1) Header
- **服务名称**: `regime_validate`（`cmd/validate`）
- **核心语言**: Go
- **主要依赖**: `provider/mock`(封装场景), `market/trend`, `alpha/registry`, `adaptive`, `engine`

## 2) Code Fingerprints (代码指纹)
### 代表性函数签名（3个）
1. `func buildPhases() []phaseSpec`
2. `func (sp *ScenarioProvider) GetRealtime(symbols []string) map[string]*core.Quote`
3. `func (ir *InstrumentedRegistry) KillSwitchReport() map[string]ksStatus`

### 参数命名规范
- 类型名强调可观测性：`ScenarioProvider`, `RegimeTracker`, `InstrumentedPerfTracker`, `InstrumentedRegistry`。
- 函数参数常用 `inner` 包装模式，表示 decorator 组合。

## 3) Context Anchors (上下文锚点)
### 全局常量
- `runTicks`, `indexSym`, `ksWindow`, `ksThreshold`, `ksCooldown`

### 异常/错误锚点
- 验证任务以“报告完整性”为第一目标；结构化输出为 contract，不可随意删段落。
- 关键失败（引擎异常/超时）必须明确退出，不隐藏。

### 必须引用的内部模块
- `trend.New(...)`（市场状态判定）
- `registry.New(...)` + 权重快照
- Kill Switch 统计相关 instrumented 结构

## 4) Implementation SOP (实现标准)
### 异步要求
- 场景 provider 必须可在 `runTicks` 后主动取消 context，确保精确停止。
- 包装器状态记录（regime/trade）需要锁保护，避免并发竞态。

### 安全要求
- Kill Switch 逻辑必须保留“触发-冷却-恢复”完整生命周期。
- 任何策略健康结论必须基于交易窗口统计，不允许主观硬编码。

### 性能要求
- 统计逻辑以增量记录为主，避免每个 tick 全量回扫全部交易。
- 输出阶段集中在 run 结束后，运行中保持轻量。

## 5) Codex Scaffolding (注释模板)
```go
// <service:regime_validate>
// <intent>实现/修改 Regime 验证与策略健康评估</intent>
// <constraints>
// 1) 保持 300 tick 场景可复现与精确停止
// 2) Kill Switch 触发/恢复语义不能改变
// 3) 报告结构（分段验证项）保持可比性
// </constraints>
// <risk-check>输出 regime 分层收益、策略健康、最终通过率</risk-check>
```

