# Skill: daily_alpha

## 1) Header
- **服务名称**: `daily_alpha`（`cmd/daily_alpha` + `alpha/daily`）
- **核心语言**: Go
- **主要依赖**: `alpha/universe`, `store(pg x/pgxpool)`, `context`

## 2) Code Fingerprints (代码指纹)
### 代表性函数签名（3个）
1. `func Run(ctx context.Context, st *store.Store, cfg Config) (Result, error)`
2. `func DefaultConfig() Config`
3. `func Open(ctx context.Context, cfg Config) (*Store, error)`（`store/db.go`）

### 参数命名规范
- 任务配置字段统一 `TopLayer1/TopLayer2/ScanTimeoutSecs`。
- 环境变量读取统一 `envOrDefault/envInt`，参数落入 `daily.Config`。

## 3) Context Anchors (上下文锚点)
### 全局常量
- 运行时依赖环境变量：`PG_DSN`, `TOP_LAYER1`, `TOP_LAYER2`, `SCAN_TIMEOUT`

### 异常/错误锚点
- 核心失败链路标准化：`FetchAll` / `UpsertAlphaRankings` 错误上抛并终止。
- 数据量保护：`res.Total < 100` 直接报错中止。

### 必须引用的内部模块
- `universe.NewFetcher().FetchAll`
- `universe.ScoreAll` + `universe.FilterLayer2`
- `store.UpsertAlphaRankings`

## 4) Implementation SOP (实现标准)
### 异步要求
- 任务必须尊重 `ctx` 取消；外层必须设置 `WithTimeout`。
- DB 连接使用 `pgxpool`，并限制 `MaxConns`（命令模式默认 3）。

### 安全要求
- 写库仅通过 `Upsert`，禁止先删后插破坏历史幂等。
- 选股过滤规则（板块前缀/成交量约束）只能通过 `Config` 开关控制。

### 性能要求
- 全市场抓取后仅做一次排序/截断，避免重复全量扫描。
- 连接池配置必须显式，禁止每步新建连接。

## 5) Codex Scaffolding (注释模板)
```go
// <service:daily_alpha>
// <intent>实现/修改每日选股流水线</intent>
// <constraints>
// 1) 全流程必须可被 context 超时取消
// 2) 只通过 store.UpsertAlphaRankings 写入结果
// 3) TopLayer1/TopLayer2/过滤规则走 Config，不写死
// </constraints>
// <risk-check>校验 total>=100、写库成功、耗时指标可输出</risk-check>
```

