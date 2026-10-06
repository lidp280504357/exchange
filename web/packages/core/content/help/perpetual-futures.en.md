---
title: Perpetual futures basics
category: futures
order: 1
summary: USDT- and coin-margined contracts, margin modes, leverage and risk limits, the mark price, funding, liquidation, reduce-only orders and take profit / stop loss.
---

A perpetual contract lets you hold a large position with a smaller amount of money (the margin), and gain or lose on rises and falls alike. Perpetuals here come in two kinds, neither with an expiry:

- **USDT-margined** (the BTCUSDT perpetual, say): linear contracts with USDT as margin; margin, profit and loss and fees are in USDT, amounts in the coin;
- **coin-margined** (the BTCUSD perpetual, say): inverse contracts with the coin as margin, traded in whole contracts, with margin, profit and loss and fees in that coin; see "Coin-margined contracts" below.

> Futures are far riskier than spot: leverage magnifies gains and losses alike, and a small move against you can liquidate the position.

## Before you start

1. Move USDT (for a coin-margined contract, its coin, such as BTC) from your spot account to your futures account under [Transfer](/assets/transfer).
2. Open a contract (for example the [BTCUSDT perpetual](/futures/BTC-USDT-PERP)) and check the margin mode, the position mode and the leverage.
3. Go long (buy) or short (sell) with a price and an amount.

## Margin modes

| Mode | Where the margin comes from | The most you can lose |
|:--|:--|:--|
| Cross | The whole available futures balance, shared by all cross positions | Possibly the whole futures balance |
| Isolated | Margin set aside for each position; you can add or remove some | That position's margin |

Cross is the default. To switch the margin mode or the position mode, first close the contract's positions and cancel its orders.

## Position modes

- **One-way** (default): one net position per contract; buying reduces a short first, selling reduces a long first.
- **Hedge**: longs and shorts are held separately, each with its own entry price, margin and profit; each order says whether it opens or closes.

## Leverage and risk limits

Leverage goes from 1x to the contract's maximum, 20x by default (a contract's maximum when it is lower). The BTCUSDT and ETHUSDT perpetuals go to 125x; the other contracts have Binance's tiers (75x or less for most), and the leverage setting on the trading page shows the position limit at the leverage chosen. Larger positions allow less leverage and need a higher maintenance margin rate. For the BTCUSDT and ETHUSDT perpetuals:

| Max leverage | Position up to | Maintenance margin rate |
|--:|--:|--:|
| 125x | 50,000 USDT | 0.4% |
| 100x | 250,000 USDT | 0.5% |
| 50x | 1,000,000 USDT | 1% |
| 20x | 5,000,000 USDT | 2.5% |
| 10x | 20,000,000 USDT | 5% |
| 5x | 50,000,000 USDT | 10% |
| 2x | 100,000,000 USDT | 12.5% |

Position sizes are notional values at the mark price, open orders on the same side included; a coin-margined contract's limits are in its coin (see below).

- **Initial margin** = notional value ÷ leverage.
- **Maintenance margin** = notional value × maintenance margin rate.

## Mark price and profit

Unrealized profit, liquidation and (by default) take profit / stop loss use the **mark price**, not the last trade:

- the **index price** comes from the major spot markets;
- the **mark price** adds the order book's basis, smoothed over 30 seconds, to the index, never more than 1% away from it.

So a spike from one or two odd trades does not liquidate anyone by itself.

- Long unrealized profit = amount × (mark price − entry price)
- Short unrealized profit = amount × (entry price − mark price)

## Funding

Perpetuals never expire; **funding** keeps their price close to the spot price:

- It is settled every 8 hours: 00:00, 08:00 and 16:00 UTC.
- Only positions **held at that moment** pay or receive it; close before and it does not apply.
- Funding = notional value × funding rate. A positive rate means longs pay shorts; a negative one, shorts pay longs.
- The rate comes from the period's average premium plus an interest term (0.01% per 8 hours), capped at ±0.75%. The trading page shows the estimate and a countdown to the next settlement.

Funding passes between users; the platform keeps none of it.

## Liquidation

- When a position's margin balance (isolated: its margin plus unrealized profit; cross: the whole futures account) falls to **1.2 times** the maintenance margin, you get a warning.
- At the **maintenance margin**, liquidation starts: related orders are canceled and the position is taken over and closed with limit orders.
- When an isolated position is liquidated, what remains of its margin goes to the insurance fund, which also covers losses beyond the margin.
- If the insurance fund cannot, opposite positions with the most profit and leverage are **auto-deleveraged (ADL)**.

The trading page shows each position's **estimated liquidation price**. Lower leverage, more isolated margin or a smaller position move it further away.

## Order types

- **Limit**: at your price, within the contract's price band around the mark price (5% for the BTC and ETH contracts; the others have Binance's, at most 15%).
- **Market**: fills at once within a protective price around the mark price; what cannot fill is canceled.
- **Reduce only**: the order only closes; its amount may not exceed the part of the position not already covered by other closing orders.

## Take profit and stop loss

A position can have take-profit and stop-loss conditional orders, up to 20 per contract:

- they trigger on the **mark price** (default) or the **last price**;
- for a long, take profit triggers at or above its price and stop loss at or below; the other way round for a short;
- when triggered, a reduce-only closing order goes out at the market (default) or at your price, closing the whole remaining position unless you set an amount;
- a trigger price the market has already passed is refused (place an order instead); if the position is gone when it triggers, the conditional order is canceled.

## Coin-margined contracts

Coin-margined contracts (the [BTCUSD perpetual](/futures/BTC-USD-PERP), say) take the coin as margin; switch to them at the top of the trading page or in the COIN-M group of the futures menu:

- **Whole contracts**: each contract is a fixed USD face value (100 USD for BTC's, 10 USD for the others), and amounts are whole contracts; the order form shows about what they are worth in the coin and in USD.
- **Settled in the coin**: margin, fees, profit and loss and funding are in that coin, from its own futures account — before trading the BTCUSD perpetual, move BTC to your futures account under [Transfer](/assets/transfer?asset=BTC). Each coin's futures account stands alone; cross margin is shared only by the positions in the same coin.
- **Position value**: contracts × face value ÷ price, in the coin. The higher the price, the less coin a contract is worth.
- **Profit and loss**: a long's unrealized profit = contracts × face value × (1 ÷ entry price − 1 ÷ mark price), the other way round for a short; it is in the coin, and the position card also shows its USD estimate at the mark price. Even without a loss on the position, a falling coin price lowers the margin's value in USD.
- **Risk limits in the coin**: at 125x, the BTCUSD perpetual holds at most 5 BTC worth of position.
- **Funding** = contracts × face value × funding rate ÷ mark price, in the coin, settled at the same times as the USDT-margined contracts.
- **What opening reserves**: each contract's margin and fee rounded up to the coin's decimals; a buy reserves at the lower of its price and the mark price (the lower the price, the more coin a contract is worth).

Liquidation warnings, liquidations and auto-deleveraging come as notifications and mails, their amounts in the contract's settlement asset (USDT, or the coin of a coin-margined contract).

## Reduce-only state

If the mark price cannot be computed for 10 seconds (a price source is down, for example), the contract becomes **reduce only**: positions can be closed but not opened. An administrator lifts it once prices are back, and the trading page shows a banner meanwhile.
