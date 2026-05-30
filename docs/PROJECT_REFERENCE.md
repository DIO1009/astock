# AStock Trade 项目参考文档

> 自动生成于 2026-05-24，用于后续开发/修改时的上下文参考。

---

## 1. 项目概述

**A 股量化交易系统**（Go 1.25），支持回测模拟（`main.go`）与实盘模拟交易两种模式（`cmd/paper`）。系统以 Tick 为周期，在每日交易时段内按间隔拉取行情、评分、下单执行。

- 语言：Go 1.25.0
- 外部依赖：`github.com/gorilla/websocket`、`github.com/jackc/pgx/v5`
- 数据源：东方财富 push2 API（实时） / CSV 回放 / Mock 合成
- 数据库：PostgreSQL（可选，默认 DSN `postgres://postgres:dmrxlbol123@127.0.0.1:5432/astock_trade`）
- 前端：React + TypeScript + Vite + Tailwind + Recharts（`dashboard/frontend`）

---

## 2. 入口与构建

| 命令 | 说明 |
|---|---|
| `go run .` | 模拟模式（200 tick，mock 数据） |
| `go build -o bin/paper_trader ./cmd/paper` | 构建 Paper Trading 程序 |
| `bash scripts/start.sh` | 启动 Paper Trading |
| `bash scripts/stop.sh` | 停止 Paper Trading |
| `ASTOCK_LIVE_DATA=1 bash scripts/start.sh` | 实时模式（东方财富 push2） |
| `ASTOCK_LIVE_DATA=1 ASTOCK_DYNAMIC_SCREENER=1 ASTOCK_MAX_POS=10 bash scripts/start.sh` | 动态选股实时模式 |

测试：
```bash
go test ./...                         # 全部测试
go test ./engine/ -run TestXxx -v    # 单测
go test ./cmd/paper/ -run TestProtectionScope -v
```

---

## 3. 架构总览

### 3.1 核心设计原则

- **接口契约层**：`core/interfaces.go` 和 `core/types.go` 定义所有模块边界和共享域类型。`core/` 包不依赖任何其他内部包，所有其他包依赖 `core/`。
- **组合根模式**：两个入口 `main.go` 和 `cmd/paper/main.go` 各自组装依赖并注入 `engine.Engine`。
- **可选组件通过 Setter 注入**：`SetMonitor`、`SetSafetyGuard`、`SetDashboard`、`SetAdaptiveOptimizer`、`SetRotationPolicy`、`SetCalendar`、`SetDataChecker`。

### 3.2 包结构

```
astock_trade/
├── main.go                  # 模拟模式入口（200 tick mock）
├── core/                    # 接口 + 类型定义（零内部依赖）
│   ├── interfaces.go        # 所有接口契约
│   └── types.go             # 领域类型（Quote, Position, Order, Signal 等）
├── engine/                  # 核心引擎（Tick 循环编排）
│   └── engine.go
├── cmd/
│   ├── paper/               # Paper Trading 入口
│   │   ├── main.go          # 组合根（实时/回放模式）
│   │   ├── alpha_scheduler.go # 每日自动选股调度
│   │   ├── runtime_restore.go # 启动状态恢复
│   │   └── safety_config.go
│   ├── backtest/            # 回测入口
│   ├── daily_alpha/         # 独立全市场选股工具
│   ├── fetchdata/           # 数据下载工具
│   ├── score/               # 评分工具
│   ├── stress/              # 压力测试
│   ├── validate/            # 验证工具
│   └── t1check/             # T+1 检查
├── alpha/                   # 策略层
│   ├── momentum/            # 动量策略（5d + 20d）
│   ├── reversal/            # 反转策略（均值回归）
│   ├── breakout/            # 突破策略（量价突破）
│   ├── volume/              # 相对成交量策略
│   ├── volatility/          # 波动率惩罚
│   ├── registry/            # 策略注册与动态权重
│   ├── daily/               # 全市场日度选股 Job
│   ├── universe/            # 全市场数据拉取 + 评分
│   ├── aggregator/          # 信号聚合
│   └── matrend/             # MA 趋势策略
├── provider/                # 行情数据提供者
│   ├── eastmoney/           # 东方财富实时行情（push2 API）
│   ├── replay/              # CSV 历史回放
│   ├── mock/                # 模拟合成数据
│   └── stress/              # 压力测试数据
├── screener/                # 股票筛选器
│   ├── static/              # 固定列表筛选
│   ├── dynamic/             # 基于 DB alpha_rankings 的动态筛选
│   └── universe/            # 全市场 Universe
├── signal/                  # 信号处理
│   ├── dampener/            # 防垄断衰减器
│   └── stability/           # 信号稳定性确认
├── decision/topn/           # TopN 选股决策
├── position/                # 持仓管理（含盈亏退出信号）
├── portfolio/               # 资金管理（仓位分配）
├── execctrl/                # 执行纪律控制
├── executor/
│   ├── simulated/           # 模拟执行（零成本）
│   └── realistic/           # 真实成本执行（含费率模型）
├── broker/paper/            # Paper Trading Broker（包装 Executor）
├── rotation/                # 轮动调仓策略
├── safety/                  # 最终安全控制层
├── monitor/                 # 实时监控与告警
├── adaptive/                # 自适应优化器
├── risk/                    # Portfolio Risk Engine
├── analysis/deviation/      # 实盘偏差分析
├── dashboard/               # Dashboard HTTP/WS 服务
├── store/                   # PostgreSQL 持久化
├── report/                  # 每日策略报告生成
├── datacheck/               # 数据完整性校验
├── calendar/                # A 股交易日历（2020-2030）
├── market/trend/            # 三态市场趋势过滤器
├── logger/                  # 日志（console / execution / filelog）
├── review/weekly/           # 周度复盘
├── config/                  # 配置文件
│   ├── trading_cost.json    # 交易费率
│   └── safety.json          # 安全控制参数
└── scripts/                 # 运维脚本
```

---

## 4. 核心 Tick 循环流程（engine/engine.go）

每个 Tick（仅在交易时段 09:30-11:30, 13:00-15:00 CST）：

```
1. AdvanceTick → ExecController + SafetyGuard
2. AdvanceTradeDay → PositionManager（T+1 解锁）
3. FactorDiag（若启用则仅诊断不交易，然后退出）
4. 获取行情：Screener.Screen() + 持仓股 → DataProvider.GetRealtime()
5. 数据完整性校验：DataChecker.Check()（失败时禁止开仓，平仓不受影响）
6. 强制清仓：SafetyGuard.ShouldForceLiquidate() → 全部 SELL
7. 止盈/止损/移动止盈：PositionManager.CheckExit() → SELL 执行
8. 市场状态判定：MarketFilter.State() + AllowOpen()
9. Alpha 评分：AlphaEngine.Rank() → SignalAdjuster.Adjust() → SignalStabilizer.Stabilize()
10. 轮动调仓：processRotation()（若启用 RotationPolicy）
11. 开仓决策：PortfolioDecision.Decide()（受制于市场过滤 + SafetyGuard + DataCheck + regimeMinScore）
12. 执行订单：Executor.Execute() → Trade 记录
13. 计算权益：equity = cash + Σ(position.Qty × currentPrice)
14. 更新：PerformanceTracker / Monitor / Dashboard / AdaptiveOptimizer
```

**T+1 规则**：当日买入的头寸 `SellableQty=0`；次日开盘 `AdvanceTradeDay()` 将其设为 `Quantity`。

---

## 5. 核心策略层（alpha/）

### 5.1 五因子策略

| 策略 | 基础权重 | 信号区间 | 说明 |
|---|---|---|---|
| `momentum` | 0.30 | [-1,+1] | 多周期趋势跟踪（5d×0.4 + 20d×0.6） |
| `reversal` | 0.25 | [-1,+1] | EMA 偏离均值回归 |
| `breakout` | 0.20 | [-1,+1] | 量价突破（成交量确认） |
| `volume` | 0.15 | [-1,+1] | 相对成交量（机构参与度代理） |
| `volatility` | 0.10 | [-1,0] | 纯风险折扣因子 |

### 5.2 Registry 动态权重（alpha/registry/）

每 5 tick（Paper Trading）或 20 tick（模拟）重新评估权重：
- `Lambda=0.40`：权重最多偏离基准 ±40%
- `MinFactor=0.20`：防止归零
- `MaxFactor=3.0`：防止垄断

### 5.3 信号处理管线

```
AlphaEngine.Rank() → SignalAdjuster.Adjust() → SignalStabilizer.Stabilize()
```

- **SignalAdjuster**（防垄断）：同一股票连续 3 tick 排名 #1 → 分数 ×0.6
- **SignalStabilizer**（稳定性）：Top-2 需连续 2 tick 才能变为稳定买入候选

### 5.4 全市场选股（alpha/daily/ + alpha/universe/）

- `alpha/universe/fetcher.go`：通过东方财富 clist API 获取全 A 股数据
- `alpha/universe/scorer.go`：综合评分（Ret5d/Ret20d/Turnover/VolumeRatio/MktCap）
- `alpha/daily/job.go`：日度选股 Job → 写入 `alpha_rankings` 表
- `cmd/paper/alpha_scheduler.go`：调度器，每日 09:00 自动运行

---

## 6. 数据提供者（provider/）

### 6.1 东方财富实时行情（provider/eastmoney/）

- **push2 API**：实时 Level-1 行情快照
- **clist API**：全市场列表数据（f109=近5日涨幅, f110=近20日涨幅, f160=近10日涨幅）
- **stock/get API**：单股行情（f170=涨跌幅×100，需正确解析：≤1000 时也要 /100）
- **PreWarm**：启动时通过腾讯财经下载 21 天历史收盘价计算 EMA20/Return5d/Volatility

### 6.2 重要：字段映射

| 东方财富字段 | 实际含义 | 代码中正确映射 |
|---|---|---|
| `f109` | 近5日涨幅 | `Ret5d` |
| `f160` | 近10日涨幅 | `Ret10d` |
| `f110` | 近20日涨幅 | `Ret20d` |
| `f170` | 涨跌幅×100 | 需统一 /100 |

### 6.3 行情 Quote 字段契约

`core.Quote` 的衍生字段（`Return5d`、`Return20d`、`EMA20`、`Volatility`、`AvgVolume5d`、`VolumeRatio`）**必须由 DataProvider 填充**，策略不得自行维护价格历史。

---

## 7. 市场过滤器（market/trend/）

三态市场状态判断，使用上证指数（000001.SH）：

| 状态 | 条件 | 行为 |
|---|---|---|
| `UPTREND` | 偏离 MA +0.5% | 正常开仓 |
| `OSCILLATE` | MA ±0.5% | 仅高质量信号开仓（Score ≥ 0.30） |
| `DOWNTREND` | 偏离 MA -0.5% | 完全禁止开仓 |

---

## 8. 保护模块清单（PROTECTED — 改动需用户同意）

> 用户明确要求：以下模块目前除非功能需要调整，**任何时候都不要改动**。必须改动时需先经过用户同意，并严格审查是否会破坏原有逻辑。

### 8.1 核心交易链路（不可改动）

- `engine/` — 核心引擎 Tick 循环编排
- `position/` — 持仓管理、止盈/止损/移动止盈信号
- `portfolio/` — 资金管理、仓位分配
- `risk/` — Portfolio Risk Engine
- `execctrl/` — 执行纪律控制（冷却期、高价重入阻止等）
- `executor/*` — 订单执行器（simulated / realistic）
- `broker/paper/` — Paper Trading Broker

### 8.2 策略/信号链路（不可改动）

- `alpha/*` — 所有 alpha 策略（momentum/reversal/breakout/volume/volatility）
- `strategy/*` — 策略评估
- `signal/*` — 信号调整（dampener/stability）
- `decision/topn` — TopN 开仓决策
- `rotation/` — 轮动调仓策略（含阈值、触发逻辑、确认机制）

---

## 9. 安全控制层（safety/）

三层保护机制：

1. **连续亏损抑制**（`config/safety.json`）：
   - streak ≥ 10 → `MaxTotalPct × 0.5`（半仓）
   - streak ≥ 15 → 停止开仓 12 tick
2. **人工控制**：SIGUSR1（停止开仓）、SIGUSR2（全部清仓）、SIGHUP（恢复开仓）
3. **执行异常检测**：延迟/成交率异常累积超标 → 自动暂停交易

---

## 10. Dashboard（dashboard/）

- HTTP 服务运行在 `:18099`
- REST API：`/api/equity`、`/api/executions`、`/api/positions`、`/api/risk-events`、`/api/system-status/latest`、`/api/candidate-pool`
- WebSocket：`/ws` 实时推送
- 前端：React + Vite + TypeScript + Tailwind + Recharts

---

## 11. 交易费率（config/trading_cost.json）

```json
{
  "commission_pct": 0.000235,   // 万2.35，买卖都收，最低5元
  "stamp_tax_pct": 0.0005,      // 万5，仅卖出收取
  "transfer_fee_pct": 0.00001,  // 万0.10，买卖都收
  "min_commission": 5.0
}
```

---

## 12. 数据库 Schema（store/schema.sql）

六张核心表：

| 表名 | 用途 |
|---|---|
| `executions` | 每笔执行记录（order_id, symbol, price, slippage, latency 等） |
| `positions` | 当前持仓快照 |
| `equity_curve` | 权益曲线 |
| `risk_events` | 风控事件 |
| `system_status` | 系统状态 |
| `alpha_rankings` | 每日 alpha 排名（`(date, symbol)` 唯一约束） |
| `daily_reports` | 每日策略报告状态 |

---

## 13. 轮动调仓（rotation/）

默认配置：
```go
RotationStartTime: "09:45"
RotationWatchRank:  70
RotationExitRank:   85      // 排名 >85 触发轮动考虑
RotationConfirmTicks: 3     // 连续 3 tick 确认
RotationConfirmDays:  2     // 连续 2 天确认
RotationDelta:        0.10
MaxRotationPerDay:    3
```

**已知问题**（来自 session notes）：
- 轮动发生在 `dataCheckOK` gating 之前/之外
- 先卖旧仓再买候选，存在卖出成功但买入失败的非原子风险
- 默认 `engine.New()` 始终启用 `rotation.New(DefaultConfig())`
- 需通过 `ASTOCK_ROTATION_ENABLED=1` 显式控制

---

## 14. 已知问题与修复记录

### 14.1 东方财富字段映射（已修复）

- `f109=近5日涨幅` → `Ret5d`
- `f160=近10日涨幅` → `Ret10d`  
- `f110=近20日涨幅` → `Ret20d`

### 14.2 实时行情 f170 解析（已修复）

- `f170` 是涨跌幅×100，需统一 /100（之前只对 >1000 或 <-1000 做除法，导致多数值放大 100 倍）

### 14.3 day_alpha 调度器交易日历（已修复）

- 之前按自然日调度，现使用 `calendar.IsTradeDay` + CST 时间

### 14.4 候选人池新股过滤

- 之前未排除名称以 C/N 开头的新股/次新股，导致极端涨幅值污染评分

### 14.5 轮动调仓 Top-85 再次出现

- `engine.New()` 默认启用 rotation，`RotationExitRank=85`
- 需要候选池 Top-100 + RotationExitRank 对齐 100 才能避免轮动

---

## 15. 环境变量速查

| 变量 | 默认 | 说明 |
|---|---|---|
| `ASTOCK_LIVE_DATA` | `0` | `1`=东方财富实时行情 |
| `ASTOCK_TICK_SECONDS` | `300` | Tick 间隔（秒） |
| `ASTOCK_DYNAMIC_SCREENER` | `0` | `1`=动态选股 |
| `ASTOCK_TOP_N` | `100` | 动态筛选 Top N |
| `ASTOCK_MAX_POS` | `10` | 最大持仓数 |
| `ASTOCK_ROTATION_ENABLED` | `0` | 启用轮动调仓 |
| `ASTOCK_ROTATION_START` | `09:45` | 轮动开始时间 |
| `ASTOCK_ROTATION_EXIT_RANK` | `85` | 轮动退出排名 |
| `ASTOCK_DATA_PATH` | — | 指定 CSV 数据文件 |
| `ASTOCK_DB_DSN` | postgres 默认 | DB 连接字符串（`-` 禁用） |
| `FACTOR_DIAG` | `0` | `1`=因子诊断模式（仅输出，不交易） |

---

## 16. 文件约定

| 文件 | 用途 |
|---|---|
| `paper_trades.jsonl` | 交易日志 |
| `paper_executions.jsonl` | 执行日志 |
| `position_state.jsonl` | 持仓快照（重启恢复） |
| `real_market_data.csv` | 真实行情数据 |
| `paper_data.csv` | 合成 fallback 数据 |
| `backtest_data.csv` | 回测数据 |
