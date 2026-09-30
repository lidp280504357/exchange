---
title: Fees
category: trading
order: 2
summary: Spot makers and takers pay 0.10%; perpetual futures 0.02% for makers and 0.05% for takers. Deposits and transfers are free; withdrawals pay a fixed fee per network.
---

## Spot trading

| Role | Fee | Paid in |
|:--|--:|:--|
| Maker | 0.10% | The coin you receive |
| Taker | 0.10% | The coin you receive |

- **Buying** pays in the base coin: buy 0.001 BTC at 0.1% and 0.000999 BTC arrives.
- **Selling** pays in the quote coin: a 100 USDT sale brings 99.9 USDT.
- Fees are set per pair; the order form shows the estimated fee.

## Perpetual futures

| Role | Fee | Paid in |
|:--|--:|:--|
| Maker | 0.02% | USDT |
| Taker | 0.05% | USDT |

- Charged on the traded value (amount × price), when opening and when closing.
- An opening order reserves the taker fee; whatever a maker fill does not use is returned.
- **Funding** is not a fee: it passes between longs and shorts every 8 hours and the platform keeps none of it. See [Perpetual futures basics](/help/perpetual-futures).
- **Liquidation** has no separate fee: its cost is part of the maintenance margin rate. When an isolated position is liquidated, what remains of its margin goes to the insurance fund.

## Deposits, withdrawals and transfers

| Item | Cost |
|:--|:--|
| Deposits | Free |
| Transfers between spot and futures | Free |
| Withdrawing ETH (Sepolia) | 0.0002 ETH each, on top of the amount |
| Withdrawing to another user's deposit address | Free (completed on the site, not on chain) |

Withdrawal fees are fixed per network, whatever the amount; the withdrawal page always shows the current one.

> This site is a test environment for learning; every balance is simulated.
