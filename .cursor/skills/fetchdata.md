# Skill: fetchdata

## 1) Header
- **服务名称**: `fetchdata`（`cmd/fetchdata`）
- **核心语言**: Go
- **主要依赖**: `net/http`, `encoding/csv`, EastMoney K 线 API

## 2) Code Fingerprints (代码指纹)
### 代表性函数签名（3个）
1. `func newClient() *http.Client`
2. `func fetchKlines(client *http.Client, symbol, begDate, endDate string, limit int) ([]string, error)`
3. `func parseKline(line string) (date, open, high, low, close_ string, volume int64, err error)`

### 参数命名规范
- API 参数使用短语义名：`begDate`, `endDate`, `limit`, `symbolsFlag`, `daysFlag`。
- 输出字段命名与 CSV 列完全对齐：`date,symbol,open,high,low,close,volume`。

## 3) Context Anchors (上下文锚点)
### 全局常量
- `klineURL`, `defaultSymbols`, `defaultDays`, `defaultOutput`

### 异常/错误锚点
- HTTP 非 200、JSON 解析失败、空 klines、字段不足都返回显式 `error`。
- `parseKline` 强制校验价格有效性与成交量可解析。

### 必须引用的内部模块
- 无内部业务模块强依赖（独立采集工具），但输出格式必须兼容 `provider/replay`。

## 4) Implementation SOP (实现标准)
### 异步要求
- 当前为串行抓取；新增并发抓取必须限制并发数并保证输出稳定排序。

### 安全要求
- 必须保留浏览器头与 secid 规则，避免 API 风控导致大面积失败。
- 不能移除 `Proxy:nil` 直连策略（该工具核心前提）。

### 性能要求
- HTTP 客户端复用连接，保留 `MaxIdleConns/IdleConnTimeout`。
- 批量写 CSV 时一次性排序后落盘，避免重复 I/O。

## 5) Codex Scaffolding (注释模板)
```go
// <service:fetchdata>
// <intent>实现/修改历史行情抓取逻辑</intent>
// <constraints>
// 1) 输出 CSV 列必须保持 replay 兼容
// 2) 保留 secid 映射与代理绕过策略
// 3) 所有解析/网络失败必须返回可定位 error
// </constraints>
// <risk-check>校验每个 symbol 至少一条 kline，且 close/volume 合法</risk-check>
```

