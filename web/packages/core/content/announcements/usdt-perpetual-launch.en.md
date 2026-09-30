---
title: USDT perpetual futures are live
date: 2026-09-30
category: product
summary: BTCUSDT and ETHUSDT perpetuals are open for trading, margined in USDT, with up to 50x leverage and funding every 8 hours.
---

USDT-margined perpetual futures are now open on the Astras test environment, starting with two contracts:

| Contract | Max leverage | Min amount | Price step | Fees (maker / taker) |
|:--|--:|--:|--:|--:|
| BTCUSDT perpetual | 50x | 0.001 BTC | 0.1 USDT | 0.02% / 0.05% |
| ETHUSDT perpetual | 50x | 0.01 ETH | 0.01 USDT | 0.02% / 0.05% |

## The rules at a glance

- **Linear contracts**: margin, profit and loss and fees are in USDT; amounts are in BTC or ETH; there is no expiry.
- **Leverage and risk limits**: 1x to 50x, 20x by default. Larger positions allow less leverage: a BTCUSDT position up to 50,000 USDT of notional value may use 50x, up to 250,000 USDT 20x.
- **Margin and position modes**: cross or isolated margin; one-way or hedge positions. One-way and cross by default.
- **Mark price**: unrealized profit and liquidations use the mark price. It starts from the index price and adds the order book's basis smoothed over 30 seconds, never more than 1% away from the index, so one or two odd trades cannot liquidate you.
- **Funding**: settled every 8 hours (00:00, 08:00 and 16:00 UTC) on the positions held at that moment. When the rate is positive, longs pay shorts; when negative, shorts pay longs.
- **Take profit and stop loss**: attach conditional orders to a position; they trigger on the mark price by default.
- **Reduce only**: such an order can only close; its amount may not exceed the part of the position not already covered by other closing orders.

## Getting started

1. Move USDT from your spot account to your futures account under [Transfer](/assets/transfer).
2. Open the [BTCUSDT perpetual](/futures/BTC-USDT-PERP), check the margin mode and leverage, and place an order.

Futures are risky: the higher the leverage, the smaller the price move that liquidates a position. Please read [Perpetual futures basics](/help/perpetual-futures) first.

> This site is a test environment for learning; every balance is simulated.
