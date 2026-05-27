#!/usr/bin/env python3
"""
AStock Trading System — 真实行情数据下载脚本（腾讯财经 K 线 API）

数据来源：腾讯财经 web.ifzq.gtimg.cn（国内直连，无需 VPN）
价格类型：日线，不复权（bfq），与券商行情一致
代理绕过：Session(trust_env=False, proxies={}) 强制直连

用法：
  python3 scripts/fetch_data.py                         # 默认：120 交易日
  python3 scripts/fetch_data.py --days 60               # 指定天数
  python3 scripts/fetch_data.py --symbols 600519 000858 # 指定标的
  python3 scripts/fetch_data.py --output mydata.csv     # 指定输出路径

依赖：
  pip3 install requests   （通常已内置，无需额外安装）
"""

import os
import sys
import csv
import datetime
import argparse
import json

try:
    import requests
except ImportError:
    print("错误：未安装 requests，请执行: pip3 install requests")
    sys.exit(1)

# ── 常量 ──────────────────────────────────────────────────────────────────────

# 腾讯财经历史 K 线接口（日线，支持不复权）
TENCENT_API = "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get"

DEFAULT_SYMBOLS = ["600519", "000858", "300750", "000300"]

SYMBOL_NAMES = {
    "600519": "贵州茅台",
    "000858": "五粮液",
    "300750": "宁德时代",
    "000300": "沪深300",
    "601318": "中国平安",
    "000001": "平安银行",
    "600036": "招商银行",
    "600276": "恒瑞医药",
    "002594": "比亚迪",
    "601888": "中国中免",
}

# ── 代码转换 ──────────────────────────────────────────────────────────────────

# 明确标记为上交所的 000xxx 指数代码（避免与深交所同号段股票混淆）
_SHANGHAI_INDEX_CODES = frozenset({
    "000001",  # 上证综指
    "000011",  # 上证基金
    "000012",  # 国债指数
    "000016",  # 上证50
    "000017",  # 新综指
    "000300",  # 沪深300
    "000688",  # 科创50
    "000852",  # 中证1000
    "000903",  # 中证100
    "000904",  # 中证200
    "000905",  # 中证500
    "000906",  # 中证800
    "000985",  # 中证全指
})


def to_tencent_symbol(code: str) -> str:
    """
    将 6 位代码转换为腾讯财经交易所前缀格式。
      sh（上交所）: 60xxxx, 68xxxx, 9xxxxx，以及已知 000xxx 上证指数
      sz（深交所）: 000xxx 股票, 001xxx, 002xxx, 003xxx, 30xxxx
    注意: 000858(五粮液) → sz000858；000300(沪深300) → sh000300
    """
    if code in _SHANGHAI_INDEX_CODES:
        return "sh" + code
    if code.startswith(("6", "9")):
        return "sh" + code
    return "sz" + code

# ── HTTP 会话（强制直连，禁用 VPN 代理）────────────────────────────────────────

def make_session() -> requests.Session:
    """
    创建禁用所有代理的 Session。
      trust_env=False : 忽略 http_proxy/https_proxy 等环境变量
      proxies={}      : 清空 Session 内部代理映射（覆盖 macOS 系统级代理）
    同时移除当前进程所有代理相关环境变量，避免 urllib3 的二次读取。
    """
    for _v in ['http_proxy', 'https_proxy', 'all_proxy', 'ftp_proxy',
               'HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'FTP_PROXY']:
        os.environ.pop(_v, None)

    s = requests.Session()
    s.trust_env = False
    s.proxies    = {}
    s.headers.update({
        'User-Agent': (
            'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) '
            'AppleWebKit/537.36 (KHTML, like Gecko) '
            'Chrome/123.0.0.0 Safari/537.36'
        ),
        'Referer': 'https://gu.qq.com/',
        'Accept':  'application/json, text/javascript, */*',
    })
    return s

# ── 数据获取 ──────────────────────────────────────────────────────────────────

def fetch_klines(
    session: requests.Session,
    symbol: str,
    start_date: datetime.date,
    end_date: datetime.date,
    limit: int = 500,
):
    """
    从腾讯财经获取单支股票的日线 K 线数据（不复权）。

    返回 list of dict: {date, open, high, low, close, volume}
    volume 单位已转换为股（手 × 100）。
    """
    tx_sym    = to_tencent_symbol(symbol)
    start_str = start_date.strftime("%Y-%m-%d")
    end_str   = end_date.strftime("%Y-%m-%d")

    # 腾讯 API: param=sh600519,day,2025-01-01,2026-03-23,500,bfq
    param = f"{tx_sym},day,{start_str},{end_str},{limit},bfq"

    resp = session.get(TENCENT_API, params={"param": param}, timeout=10)
    resp.raise_for_status()

    data = resp.json()

    # 提取 K 线数组
    # 结构: data.data.<symbol>.day 或 data.data.<symbol>.bfqday
    sym_data = data.get("data", {}).get(tx_sym, {})
    klines   = sym_data.get("day") or sym_data.get("bfqday") or []

    if not klines:
        raise ValueError(f"响应中无 K 线数据 (keys={list(sym_data.keys())})")

    rows = []
    for bar in klines:
        # 格式: [date, open, close, high, low, volume, ...]
        if len(bar) < 6:
            continue
        try:
            close_f = float(bar[2])
            if close_f <= 0:
                continue
            rows.append({
                "date":   bar[0][:10],          # YYYY-MM-DD
                "symbol": symbol,
                "open":   f"{float(bar[1]):.4f}",
                "high":   f"{float(bar[3]):.4f}",
                "low":    f"{float(bar[4]):.4f}",
                "close":  f"{close_f:.4f}",
                "volume": int(float(bar[5]) * 100),  # 手 → 股
            })
        except (ValueError, IndexError):
            continue

    return rows

# ── 主流程 ────────────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="下载 A 股/指数日线行情（腾讯财经，不复权，国内直连）"
    )
    parser.add_argument("--symbols", nargs="+", default=DEFAULT_SYMBOLS,
                        help=f"股票/指数代码（默认: {' '.join(DEFAULT_SYMBOLS)}）")
    parser.add_argument("--days", type=int, default=120,
                        help="历史交易日数量（默认: 120 ≈ 6 个月）")
    parser.add_argument("--output", default="real_market_data.csv",
                        help="输出文件路径（默认: real_market_data.csv）")
    args = parser.parse_args()

    end_date   = datetime.date.today()
    # 交易日 × 约 1.7 + 40 天缓冲 = 足够覆盖节假日
    start_date = end_date - datetime.timedelta(days=int(args.days * 1.7) + 40)

    print(f"{'='*56}")
    print(f"  AStock 真实行情下载（腾讯财经 K 线 API）")
    print(f"  数据类型: 日线，不复权（bfq）")
    print(f"  日期范围: {start_date} → {end_date}")
    print(f"  目标天数: {args.days} 交易日")
    print(f"  输出文件: {args.output}")
    print(f"  代理设置: 已强制禁用（直连 gtimg.cn）")
    print(f"{'='*56}")

    session  = make_session()
    all_rows = []
    failed   = []

    for sym in args.symbols:
        name = SYMBOL_NAMES.get(sym, sym)
        print(f"  [{sym}] {name} ... ", end="", flush=True)

        try:
            rows = fetch_klines(session, sym, start_date, end_date, args.days + 50)
            if not rows:
                raise ValueError("返回空数据")
        except Exception as e:
            print("失败")
            print(f"  [WARN] {e}")
            failed.append(sym)
            continue

        all_rows.extend(rows)
        latest_close = rows[-1]["close"] if rows else "N/A"
        print(f"{len(rows)} 条  最新收盘 {latest_close}")

    if not all_rows:
        print("\n错误：所有标的均获取失败。")
        print()
        print("可能原因与解决方案：")
        print("  1. 确认 web.ifzq.gtimg.cn 可访问:")
        print("     curl -s 'https://web.ifzq.gtimg.cn/' | head -5")
        print("  2. 确认网络连接正常（非航班/酒店网络）")
        print("  3. 临时关闭 VPN 后重试")
        print("  4. 改用实时模式（不依赖历史 CSV）：")
        print("     export ASTOCK_LIVE_DATA=1 && bash scripts/start.sh")
        sys.exit(1)

    # 按日期 + 代码排序
    all_rows.sort(key=lambda r: (r["date"], r["symbol"]))

    with open(args.output, "w", newline="", encoding="utf-8") as f:
        writer = csv.DictWriter(
            f, fieldnames=["date", "symbol", "open", "high", "low", "close", "volume"]
        )
        writer.writeheader()
        writer.writerows(all_rows)

    print(f"\n{'='*56}")
    print(f"  ✅ 完成：共 {len(all_rows)} 条记录 → {args.output}")
    if failed:
        print(f"  ⚠️  以下标的失败（系统降级使用合成数据）: {', '.join(failed)}")
    print(f"{'='*56}")
    print()
    print("  启动时系统自动加载此文件（优先于合成数据）。")
    print(f"  更新数据：python3 scripts/fetch_data.py")


if __name__ == "__main__":
    main()
