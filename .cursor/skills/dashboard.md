# Skill: dashboard

## 1) Header
- **服务名称**: `dashboard`（`dashboard/server.go` + `dashboard/frontend`）
- **核心语言**: Go + TypeScript/React
- **主要依赖**: `gorilla/websocket`, `store`, `monitor`, `risk`, `safety`, `react`, `vite`, `recharts`

## 2) Code Fingerprints (代码指纹)
### 代表性函数签名（3个）
1. `func (s *Server) OnTick(equity float64, report core.PerformanceReport, positions []core.Position, quotes map[string]*core.Quote)`
2. `func (s *Server) ListenAndServe() error`
3. `const sendCommand = useCallback((action: CommandAction) => { ... }, [])`（`frontend/src/App.tsx`）

### 参数命名规范
- 后端：Go `camelCase`，快照字段统一语义对象名（`Account`, `Risk`, `Safety`, `Execution`）。
- 前端：`camelCase` 局部状态 + `snake_case` JSON 字段（与后端 Snapshot contract 对齐）。

## 3) Context Anchors (上下文锚点)
### 全局常量
- `equitySpikeThreshold`（后端异常权益点过滤）
- 前端连接常量：`WS_URL`, `RECONNECT_DELAY`

### 异常/错误锚点
- WebSocket 编解码失败记录日志并降级，不中断主引擎。
- API 层统一 `apiJSON(..., err)` 输出错误对象。

### 必须引用的内部模块
- 后端必须依赖 `monitor`, `safety`, `risk`, `store.Writer/Store`
- 候选池构建依赖 `GetTopRankings + SetWatchList + SetSignalCache`
- 前端必须遵循 `dashboard/frontend/src/types.ts` 的 Snapshot 契约

## 4) Implementation SOP (实现标准)
### 异步要求
- 后端 Hub 模型固定：`register/unregister/broadcast` channel 驱动。
- WebSocket `writePump/readPump` 必须保留心跳与 read deadline。
- 前端必须支持断线重连，禁止单次连接失败后静默停止。

### 安全要求
- 控制命令仅允许：`stop_opening|resume_opening|force_liquidate`。
- 新命令必须在后端白名单显式声明并接入 SafetyGuard。

### 性能要求
- 快照广播非阻塞（`select default`），避免慢客户端拖垮服务端。
- 数据查询走 `store` + 超时上下文；不允许无界历史查询。
- DB 连接必须复用连接池（`pgxpool`），前端避免高频无效重渲染。

## 5) Codex Scaffolding (注释模板)
```go
// <service:dashboard>
// <intent>实现/修改交易驾驶舱后端/前端联动</intent>
// <constraints>
// 1) 保持 Snapshot 字段契约稳定（types.ts 对齐）
// 2) WS hub/readPump/writePump 心跳与超时机制不可移除
// 3) 控制命令必须白名单并映射到 SafetyGuard
// 4) DB 查询必须使用 context 超时
// </constraints>
// <risk-check>验证断线重连、命令下发、快照广播、API 回包</risk-check>
```

