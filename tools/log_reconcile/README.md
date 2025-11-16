# log_reconcile

基于币安订单自动补全/校正决策日志中的平仓记录（包含“部分平仓”）。

## 使用方法

```powershell
先扫描符号（必须先有符号，拉单才有目标）
go run ./tools/log_reconcile -action scan-symbols
按交易员从配置库拉单（新增了可见日志）
go run ./tools/log_reconcile -action fetch-orders-db -config_db config.db -user_id default -base fapi
对账
go run ./tools/log_reconcile -action reconcile
```

## 主要参数

- `-action`: `scan-symbols` / `fetch-orders` / `fetch-orders-db` / `reconcile` / `partial-close-reconcile`
- `-decision_dir`: 日志目录 (默认 `decision_logs`)
- `-db`: 对账缓存库 (默认 `tools/log_reconcile/reconcile.db`)
- `-base`: 币安合约类型，`fapi`(U本位) 或 `dapi`(币本位)，默认 `fapi`

单一密钥模式（旧方式，仅用于 `-action fetch-orders`）
- `-api_key`: 币安 API Key
- `-secret_key`: 币安 Secret Key

从配置库读取（推荐，`-action fetch-orders-db`）
- `-config_db`: 配置数据库路径 (默认 `config.db`)
- `-user_id`: 配置库中的用户ID (默认 `default`)

## 功能

- **补全**: 为缺失平仓的开仓生成 decision_reconcile_*.json。
	- 若匹配到的订单是 reduceOnly 且非 closePosition，则自动生成为 `partial_close`；
	- 否则生成为 `close_long`/`close_short`，数量与成交量一致。
- **校正**: 修正价格/数量偏差 >1% 的记录 (自动备份为 .bak)
- **隔离**: 多交易员数据独立处理

## 匹配规则说明（简要）

- 开仓匹配：仅匹配非 reduceOnly/closePosition 且 `FILLED` 的订单。
- 平仓匹配：仅匹配 `close_long`/`close_short` 且 `FILLED` 的订单。
- 部分平仓（partial_close）：匹配 `reduceOnly=true` 的订单，接受以下状态：
	- `FILLED`；
	- `PARTIALLY_FILLED` 或 `CANCELED`，且已成交数量 `executedQty > 0`。

---
v1.0 | 2025-11-12
v1.1 | 2025-11-16 新增 `fetch-orders-db`，支持从 config.db 按交易员读取密钥
