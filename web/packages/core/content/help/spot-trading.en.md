---
title: Spot trading basics
category: trading
order: 1
summary: Limit and market orders, makers and takers, which coin pays the fee, and the precision, minimum and price protection checks.
---

Spot trading means buying and selling the coins themselves. In BTC/USDT, BTC is the **base coin** (what you buy or sell) and USDT the **quote coin** (what prices and payments are in).

## Limit and market orders

| | Limit order | Market order |
|:--|:--|:--|
| You enter | A price and an amount | To buy, the USDT to spend; to sell, the BTC to sell |
| Price you get | Your price or better | The book's best prices, level by level |
| Unfilled part | Stays on the book (good till canceled by default) | Canceled at once |
| Good for | A price you choose, no rush | Filling right now |

Limit orders can also use other time-in-force options:

- **IOC**: fill what can be filled now, cancel the rest;
- **FOK**: fill everything now or cancel the whole order;
- **Post only**: refused if it would fill at once, so it only ever trades as a maker.

## Makers and takers

- An order that **does not fill at once** and waits on the book is the **maker**;
- an order that **fills at once** against orders already on the book is the **taker**.

One order can do both: the part that fills at once pays the taker fee, the rest fills later as a maker.

## Fees

Spot fees are 0.10% for makers and 0.10% for takers by default, on the traded value, **paid in the coin you receive**:

- **Buying**, the fee is in the **base coin**: buy 0.001 BTC at a 0.1% taker fee and 0.000999 BTC arrives;
- **selling**, the fee is in the **quote coin**: a sale worth 100 USDT at 0.1% brings 99.9 USDT.

Fees follow the coin's precision; a remainder below the smallest unit is rounded in the platform's favor. See [Fees](/help/fees).

## Checks before an order is accepted

- **Precision**: the price must be a whole number of price steps and the amount a whole number of amount steps; the order form follows them.
- **Minimum value**: at least 5 USDT per order on USDT pairs.
- **Price protection**: a limit price too far from the latest trade price is refused (±10% on BTC/USDT); market orders only fill within that band and cancel the rest.
- **Open orders**: at most 200 per pair and 1,000 in total.
- **Pair status**: only trading pairs take new orders; "coming soon" and "halted" pairs do not, and "cancel only" pairs only accept cancellations.

## Held funds and canceling

- An order holds what it may need: a limit buy holds price × amount of the quote coin, a market buy the amount you entered, a sell the base coin you sell.
- When a limit buy fills at a better price, the difference is released at once.
- When an order ends (filled, canceled or rejected), whatever is still held returns.
- A cancellation takes effect once the matching engine confirms it; a fully filled order cannot be canceled.

## Where to follow prices

[Markets](/markets) lists every pair's latest price, 24-hour change and turnover; click a row to open its trading page. There the order book is on the left, the chart in the middle, the order form on the right and your orders and trades below.

> This site is a test environment for learning; every balance is simulated.
