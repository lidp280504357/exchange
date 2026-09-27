# ADR-0008：金额用十进制定点数与字符串传输，ID 用 UUIDv7 与 int64 序号

- 状态：已接受（2026-09-29）
- 关联：需求 §7.1、§10.3；决策 #3、#4

## 背景

浮点数不能表示 0.1；不同资产精度不同（BTC 8 位、USDT 6 位、ETH 18 位）；交易所对外 ID 需要不可猜测，内部又需要严格有序。

## 决策

1. 领域层金额、价格、数量一律 `shopspring/decimal`；禁止 `float32/float64` 出现在金额路径。
2. PostgreSQL `NUMERIC(38,18)`，ClickHouse `Decimal128(18)`，JSON 与 Protobuf 中为十进制字符串。
3. 每个资产声明 `decimals`，每个交易对声明 tick size 与 lot size；超精度输入直接拒绝而不是四舍五入。
4. 舍入规则集中定义：手续费向平台有利方向截断；资金费付方向上取整、收方向下取整，差额入 `FUNDING_CLEARING`。
5. 对外实体 ID 用 UUIDv7（时间有序、不可猜测）；撮合 sequence 与账本分录序号用 int64 单调递增；`client_order_id` 由客户端提供、每用户唯一。

## 理由

正确性优先于极限性能；decimal 在本项目的吞吐目标下足够快。

## 后果

- 前端必须以字符串处理金额并按精度格式化，不能用 JS Number 运算。
- 属性测试覆盖舍入与精度不变量。

## 备选

int64 最小单位（快但跨资产运算易溢出、转换代码多）；Snowflake 全局 int64 ID（需发号器）。
