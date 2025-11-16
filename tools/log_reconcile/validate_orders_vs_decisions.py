#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
将币安期货订单导出与 decision_*.json 日志进行交叉校验：
- 针对日志中成功执行的开/平仓动作（decisions.success=true 或 execution_log 含 ✓ ... 成功），
  核对订单历史中对应订单的成交均价与数量。

匹配策略：
1) 首选通过决策中的 order_id 与订单的 orderId 精确匹配；
2) 若缺少 order_id，则按 symbol + 方向(open/close, long/short → 买卖+持仓方向) + 时间窗口 进行最近邻匹配；
3) 仅统计 FILLED 的订单（部分成交按 executedQty>0 计）；

输出：
- reports/orders_decisions_validation.csv 明细
- reports/orders_decisions_summary.md 摘要与异常列表

用法（PowerShell）：
  python .\validate_orders_vs_decisions.py \
    --orders .\orders_export.with_timezh.json \
    --logs-dir ..\..\decision_logs\binance_e10b9e46-2a6e-4124-9fa1-101973f1284f_deepseek_1762782649 \
    --time-tolerance-sec 180 --price-tol-pct 0.5 --qty-tol-pct 1.0
"""
from __future__ import annotations
import argparse
import csv
import datetime as dt
import json
import math
import os
import re
from dataclasses import dataclass
from typing import Any, Dict, List, Optional, Tuple


# 与 analyze_logs.py 对齐的执行成功日志格式（用于兜底识别）
EXEC_LOG_RE = re.compile(r"[✓✔]\s*([A-Z0-9]+USDT)\s+(open_long|open_short|close_long|close_short)\s*成功")


@dataclass
class DecisionOp:
    ts: dt.datetime
    symbol: str
    action: str  # open_long/open_short/close_long/close_short
    price: Optional[float]
    qty: Optional[float]
    order_id: Optional[int]
    success: bool


@dataclass
class OrderRow:
    time: dt.datetime
    order_id: int
    symbol: str
    side: str  # BUY/SELL
    position_side: Optional[str]  # LONG/SHORT/None
    reduce_only: Optional[bool]
    status: str  # FILLED/NEW/CANCELED...
    type: str
    avg_price: Optional[float]
    executed_qty: Optional[float]


def _to_dt_ms(ms: int) -> dt.datetime:
    # 币安 futures 返回 ms 时间戳（UTC）
    try:
        return dt.datetime.utcfromtimestamp(ms / 1000.0).replace(tzinfo=dt.timezone.utc)
    except Exception:
        # 回退：当前时间
        return dt.datetime.now(dt.timezone.utc)


def load_orders(path: str) -> List[OrderRow]:
    with open(path, 'r', encoding='utf-8') as f:
        data = json.load(f)
    rows: List[OrderRow] = []
    for o in data:
        try:
            if not isinstance(o, dict):
                continue
            order_id = int(o.get('orderId'))
            symbol = str(o.get('symbol'))
            side = str(o.get('side')) if o.get('side') else ''
            position_side = o.get('positionSide')
            reduce_only = o.get('reduceOnly')
            status = str(o.get('status')) if o.get('status') else ''
            typ = str(o.get('type')) if o.get('type') else ''
            avg_price = None
            if o.get('avgPrice') is not None:
                try:
                    avg_price = float(o['avgPrice'])
                except Exception:
                    avg_price = None
            executed_qty = None
            if o.get('executedQty') is not None:
                try:
                    executed_qty = float(o['executedQty'])
                except Exception:
                    executed_qty = None
            time_ms = int(o.get('time')) if o.get('time') is not None else int(o.get('updateTime') or 0)
            rows.append(OrderRow(
                time=_to_dt_ms(time_ms),
                order_id=order_id,
                symbol=symbol,
                side=side,
                position_side=str(position_side) if position_side else None,
                reduce_only=bool(reduce_only) if reduce_only is not None else None,
                status=status,
                type=typ,
                avg_price=avg_price,
                executed_qty=executed_qty,
            ))
        except Exception:
            continue
    return rows


def safe_load_json(path: str) -> Optional[Dict[str, Any]]:
    try:
        with open(path, 'r', encoding='utf-8') as f:
            return json.load(f)
    except Exception:
        return None


def parse_decision_ops(logs_dir: str) -> List[DecisionOp]:
    files = [os.path.join(logs_dir, fn) for fn in os.listdir(logs_dir) if fn.startswith('decision_') and fn.endswith('.json')]
    ops: List[DecisionOp] = []
    for fp in sorted(files):
        data = safe_load_json(fp)
        if not data:
            continue
        # 基准时间：文件中的 timestamp（带时区）
        ts_base = None
        try:
            if isinstance(data.get('timestamp'), str):
                ts_base = dt.datetime.fromisoformat(data['timestamp'].replace('Z', '+00:00'))
        except Exception:
            ts_base = None
        if ts_base is None:
            # 回退：文件修改时间
            ts_base = dt.datetime.fromtimestamp(os.path.getmtime(fp), tz=dt.timezone.utc)

        # 1) 优先 decisions 数组
        for d in data.get('decisions') or []:
            try:
                action = d.get('action')
                if action not in ('open_long', 'open_short', 'close_long', 'close_short'):
                    continue
                symbol = d.get('symbol')
                if not isinstance(symbol, str):
                    continue
                price = None
                qty = None
                order_id = None
                if d.get('price') is not None:
                    try:
                        price = float(d['price'])
                    except Exception:
                        price = None
                if d.get('quantity') is not None:
                    try:
                        qty = float(d['quantity'])
                    except Exception:
                        qty = None
                if d.get('order_id') is not None:
                    try:
                        order_id = int(d['order_id'])
                    except Exception:
                        order_id = None
                ts_dec = ts_base
                try:
                    if isinstance(d.get('timestamp'), str):
                        ts_dec = dt.datetime.fromisoformat(d['timestamp'].replace('Z', '+00:00'))
                except Exception:
                    pass
                success = bool(d.get('success')) if 'success' in d else False
                if success:
                    ops.append(DecisionOp(ts=ts_dec, symbol=symbol, action=action, price=price, qty=qty, order_id=order_id, success=True))
            except Exception:
                continue

        # 2) 兜底：从 execution_log 行识别成功动作（缺少数量/价格时也记录，用于“找单对时”）
        for line in data.get('execution_log') or []:
            if not isinstance(line, str):
                continue
            m = EXEC_LOG_RE.search(line)
            if not m:
                continue
            symbol = m.group(1)
            action = m.group(2)
            # 避免与 decisions 重复：以 decisions 为准；若 decisions 已包含 success=true 的相同动作+符号，则跳过
            if any(o.symbol == symbol and o.action == action and abs((o.ts - ts_base).total_seconds()) < 600 for o in ops):
                continue
            ops.append(DecisionOp(ts=ts_base, symbol=symbol, action=action, price=None, qty=None, order_id=None, success=True))
    return ops


def action_to_order_filters(action: str) -> Tuple[str, Optional[str], Optional[bool]]:
    """将 open/close + long/short 映射为 订单 side/positionSide/reduceOnly 的期望组合。
    返回 (side, positionSide, reduceOnly)；None 表示不强约束。
    """
    if action == 'open_long':
        return 'BUY', 'LONG', False
    if action == 'open_short':
        return 'SELL', 'SHORT', False
    if action == 'close_long':
        return 'SELL', 'LONG', True
    if action == 'close_short':
        return 'BUY', 'SHORT', True
    return '', None, None


def within_pct(a: Optional[float], b: Optional[float], tol_pct: float) -> Optional[bool]:
    if a is None or b is None:
        return None
    if a == 0 and b == 0:
        return True
    if a == 0 or b == 0:
        return False
    return abs(a - b) / max(1e-12, abs(b)) <= tol_pct / 100.0


def find_best_order_match(
    dec: DecisionOp,
    orders: List[OrderRow],
    time_tol_sec: int,
    allow_reduce_mismatch: bool = True,
) -> Optional[OrderRow]:
    side, pos_side, reduce_only = action_to_order_filters(dec.action)
    # 候选集：符号一致、方向一致、状态 FILLED、时间在窗口内
    window_start = dec.ts - dt.timedelta(seconds=time_tol_sec)
    window_end = dec.ts + dt.timedelta(seconds=time_tol_sec)
    candidates: List[OrderRow] = []
    for o in orders:
        if o.symbol != dec.symbol:
            continue
        if o.status != 'FILLED':
            continue
        if o.side != side:
            continue
        if o.time < window_start or o.time > window_end:
            continue
        if pos_side and o.position_side and o.position_side != pos_side:
            continue
        # 有些交易所返回 reduceOnly 可能为 None；若约束为 True/False，但订单字段为 None，则允许通过
        if reduce_only is not None and o.reduce_only is not None and o.reduce_only != reduce_only:
            if not allow_reduce_mismatch:
                continue
        candidates.append(o)
    if not candidates:
        return None
    # 选择时间距离最近的
    candidates.sort(key=lambda x: abs((x.time - dec.ts).total_seconds()))
    return candidates[0]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--orders', required=True, help='币安订单导出 JSON（含time与orderId）')
    ap.add_argument('--logs-dir', required=True, help='包含 decision_*.json 的目录')
    ap.add_argument('--time-tolerance-sec', type=int, default=180, help='时间匹配容差，单位秒，默认±180s')
    ap.add_argument('--price-tol-pct', type=float, default=0.5, help='价格相对误差阈值%（默认0.5%）')
    ap.add_argument('--qty-tol-pct', type=float, default=1.0, help='数量相对误差阈值%（默认1.0%）')
    ap.add_argument('--strict', action='store_true', help='严格模式：仅用order_id匹配；必须同时匹配positionSide/reduceOnly；价格与数量必须提供且在阈值内（不可为NA）')
    ap.add_argument('--from-iso', default=None, help='仅校验该时间(含)之后的决策，例如 2025-11-09T00:00:00+08:00')
    ap.add_argument('--to-iso', default=None, help='仅校验该时间(含)之前的决策，例如 2025-11-12T00:00:00+08:00')
    args = ap.parse_args()

    logs_dir = os.path.abspath(args.logs_dir)
    orders_path = os.path.abspath(args.orders)

    orders = load_orders(orders_path)
    # 为 O(1) 通过 order_id 查找
    order_by_id: Dict[int, OrderRow] = {o.order_id: o for o in orders}

    decisions = parse_decision_ops(logs_dir)

    # 时间过滤（若提供）
    def _parse_iso(s: Optional[str]) -> Optional[dt.datetime]:
        if not s:
            return None
        try:
            return dt.datetime.fromisoformat(s.replace('Z', '+00:00'))
        except Exception:
            return None

    t_from = _parse_iso(args.from_iso)
    t_to = _parse_iso(args.to_iso)
    if t_from or t_to:
        _filtered: List[DecisionOp] = []
        for d in decisions:
            if t_from and d.ts < t_from:
                continue
            if t_to and d.ts > t_to:
                continue
            _filtered.append(d)
        decisions = _filtered

    # 输出目录
    report_dir = os.path.join(logs_dir, 'reports')
    os.makedirs(report_dir, exist_ok=True)
    csv_path = os.path.join(report_dir, 'orders_decisions_validation.csv')
    md_path = os.path.join(report_dir, 'orders_decisions_summary.md')

    total = 0
    matched = 0
    pass_cnt = 0
    fail_rows: List[Tuple[str, ...]] = []

    with open(csv_path, 'w', newline='', encoding='utf-8-sig') as f:
        w = csv.writer(f)
        w.writerow([
            'ts', 'symbol', 'action', 'decision_price', 'decision_qty', 'decision_order_id',
            'order_time', 'order_id', 'side', 'positionSide', 'reduceOnly', 'avgPrice', 'executedQty',
            'price_match', 'qty_match', 'price_diff_pct', 'qty_diff_pct', 'match_method'
        ])

        for d in decisions:
            total += 1
            o: Optional[OrderRow] = None
            method = ''
            # 1) 通过 order_id 精确匹配（严格/非严格都优先用）
            if d.order_id and d.order_id in order_by_id:
                o = order_by_id[d.order_id]
                method = 'order_id'
            # 2) 严格模式：禁止时间窗口回退
            if o is None and not args.strict:
                o = find_best_order_match(d, orders, time_tol_sec=args.time_tolerance_sec,
                                          allow_reduce_mismatch=(not args.strict))
                method = 'time_window' if o else ''

            if o is None:
                w.writerow([
                    d.ts.isoformat(), d.symbol, d.action,
                    d.price if d.price is not None else '', d.qty if d.qty is not None else '', d.order_id if d.order_id else '',
                    '', '', '', '', '', '', '',
                    '', '', '', '', 'not_found'
                ])
                fail_rows.append((d.ts.isoformat(), d.symbol, d.action, '未找到匹配订单'))
                continue

            matched += 1
            price_ok = within_pct(d.price, o.avg_price, args.price_tol_pct)
            qty_ok = within_pct(d.qty, o.executed_qty, args.qty_tol_pct)

            # 严格模式下，强制匹配 positionSide 与 reduceOnly
            if args.strict:
                exp_side, exp_pos_side, exp_reduce = action_to_order_filters(d.action)
                if exp_pos_side and (o.position_side or '') != exp_pos_side:
                    qty_ok = False  # 强制失败
                if exp_reduce is not None and o.reduce_only is not None and o.reduce_only != exp_reduce:
                    qty_ok = False  # 强制失败
            price_diff_pct = ''
            qty_diff_pct = ''
            if d.price is not None and o.avg_price is not None and o.avg_price != 0:
                price_diff_pct = f"{(abs(d.price - o.avg_price) / abs(o.avg_price) * 100.0):.4f}%"
            if d.qty is not None and o.executed_qty is not None and o.executed_qty != 0:
                qty_diff_pct = f"{(abs(d.qty - o.executed_qty) / abs(o.executed_qty) * 100.0):.4f}%"

            # 统计通过/失败
            if args.strict:
                passed = (price_ok is True) and (qty_ok is True)
            else:
                passed = (price_ok in (True, None)) and (qty_ok in (True, None))
            if passed:
                pass_cnt += 1
            else:
                msg = []
                if price_ok is False:
                    msg.append(f"价格不一致: dec={d.price} vs avg={o.avg_price} ({price_diff_pct})")
                if qty_ok is False:
                    msg.append(f"数量或方向不一致: dec={d.qty} vs exec={o.executed_qty} ({qty_diff_pct})")
                fail_rows.append((d.ts.isoformat(), d.symbol, d.action, '; '.join(msg) or '字段缺失'))

            w.writerow([
                d.ts.isoformat(), d.symbol, d.action,
                d.price if d.price is not None else '', d.qty if d.qty is not None else '', d.order_id if d.order_id else '',
                o.time.isoformat(), o.order_id, o.side, o.position_side or '', o.reduce_only if o.reduce_only is not None else '',
                o.avg_price if o.avg_price is not None else '', o.executed_qty if o.executed_qty is not None else '',
                'OK' if price_ok is True else ('NA' if price_ok is None else 'NG'),
                'OK' if qty_ok is True else ('NA' if qty_ok is None else 'NG'),
                price_diff_pct, qty_diff_pct, method
            ])

    # Markdown 摘要
    with open(md_path, 'w', encoding='utf-8') as f:
        f.write('# 订单与日志决策校验摘要\n\n')
        f.write(f'- 模式: {"严格" if args.strict else "宽松"}\n')
        f.write(f'- 日志成功动作数: {total}\n')
        f.write(f'- 匹配到订单数: {matched}\n')
        f.write(f'- 校验通过数: {pass_cnt}\n')
        f.write(f'- 不通过/未匹配数: {total - pass_cnt}\n')
        f.write(f'- 订单文件: `{orders_path}`\n')
        f.write(f'- 日志目录: `{logs_dir}`\n')
        if t_from:
            f.write(f'- 起始时间: {t_from.isoformat()}\n')
        if t_to:
            f.write(f'- 截止时间: {t_to.isoformat()}\n')
        f.write('\n## 异常明细（最多前100条）\n\n')
        for row in fail_rows[:100]:
            f.write(f"- {row[0]} {row[1]} {row[2]} → {row[3]}\n")

    print(f"明细CSV: {csv_path}")
    print(f"摘要MD: {md_path}")


if __name__ == '__main__':
    main()
