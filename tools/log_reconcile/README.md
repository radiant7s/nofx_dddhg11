# log_reconcile

基于币安订单自动补全/校正决策日志中的平仓记录（包含“部分平仓”）。

## 使用方法

```powershell
# 1) 先扫描符号（必须先有符号，拉单才有目标）
go run ./tools/log_reconcile -action scan-symbols

# 2) 从 config.db 按交易员拉取订单（推荐）
# - 不指定 -exchange_id 时，会自动匹配并依次使用所有 Binance 账户（“有几条对几条”）
go run ./tools/log_reconcile -action fetch-orders-db -config_db config.db -user_id default -base fapi

# 如需要仅使用某个账户（例：exchange_id = binance）
go run ./tools/log_reconcile -action fetch-orders-db -config_db config.db -user_id default -base fapi -exchange_id binance

# 使用交割合约（dapi）示例
go run ./tools/log_reconcile -action fetch-orders-db -config_db config.db -user_id default -base dapi

# 3) 对账（生成/校正日志；自动为原文件创建 .bak 备份）
go run ./tools/log_reconcile -action reconcile

# 4) （可选）仅对“部分平仓”执行对账
go run ./tools/log_reconcile -action partial-close-reconcile

# 备选：单一密钥模式（不推荐，易混用不同交易员的订单）
go run ./tools/log_reconcile -action fetch-orders -api_key <API_KEY> -secret_key <SECRET> -base fapi








PS D:\ai\nofx-dev\tools\log_reconcile> python .\validate_orders_vs_decisions.py --orders .\orders_export.with_timezh.json --logs-dir ..\..\decision_logs\binance_e10b9e46-2a6e-4124-9fa1-101973f1284f_deepseek_1762782649 --time-tolerance-sec 240 --price-tol-pct 0.6 --qty-tol-pct 1.0
D:\ai\nofx-dev\tools\log_reconcile\validate_orders_vs_decisions.py:3: SyntaxWarning: invalid escape sequence '\o'
  """
D:\ai\nofx-dev\tools\log_reconcile\validate_orders_vs_decisions.py:67: DeprecationWarning: datetime.datetime.utcfromtimestamp() is deprecated and scheduled for removal in a future version. Use timezone-aware objects to represent datetimes in UTC: datetime.datetime.fromtimestamp(timestamp, datetime.UTC).
  return dt.datetime.utcfromtimestamp(ms / 1000.0).replace(tzinfo=dt.timezone.utc)
明细CSV: D:\ai\nofx-dev\decision_logs\binance_e10b9e46-2a6e-4124-9fa1-101973f1284f_deepseek_1762782649\reports\orders_decisions_validation.csv
摘要MD: D:\ai\nofx-dev\decision_logs\binance_e10b9e46-2a6e-4124-9fa1-101973f1284f_deepseek_1762782649\reports\orders_decisions_summary.md
PS D:\ai\nofx-dev\tools\log_reconcile> python .\add_timezh.py --input .\orders_export.json --output .\orders_export.with_timezh.json --offset-hours 8
已更新对象数量: 3663
已写入: D:\ai\nofx-dev\tools\log_reconcile\orders_export.with_timezh.json
PS D:\ai\nofx-dev\tools\log_reconcile>

```

提示：
- `-decision_dir` 默认 `decision_logs`；若日志目录不同可指定，如：`-decision_dir decision_logs/binance_*`。
- `-db` 默认 `tools/log_reconcile/reconcile.db`，采用增量写入（不会清空以往数据，使用 `last_order_id` 继续拉取）。
- `-interval_sec` 控制拉单间隔（默认 3 秒）。
- 若日志出现“未找到绑定到交易员的 Binance 密钥，尝试回退到按交易所拉取...”，工具会：
	- 先按 `traders JOIN exchanges` 尝试获取按交易员的密钥；
	- 若未命中，则回退读取 `exchanges`，自动匹配 `id/name/type` 中包含/等于 `binance`；
	- 若当前 `user_id` 下也没有，则跨用户搜索匹配到的 Binance 账户并依次使用。

## 功能

- **校正**: 修正价格/数量偏差 >1% 的记录（自动备份为 `.bak`）。
- **隔离**: 多交易员数据独立处理。
- **匹配规则**:
	- 开仓匹配：仅匹配非 reduceOnly/closePosition 且 `FILLED` 的订单；
	- 平仓匹配：仅匹配 `close_long`/`close_short` 且 `FILLED` 的订单；
	- 部分平仓（partial_close）：匹配 `reduceOnly=true` 的订单，接受：
		- `FILLED`；
		- `PARTIALLY_FILLED` 或 `CANCELED` 且 `executedQty > 0`。

---
v1.0 | 2025-11-12
v1.1 | 2025-11-16 新增 `fetch-orders-db`，支持从 config.db 按交易员读取密钥；增加回退与多账户迭代说明
