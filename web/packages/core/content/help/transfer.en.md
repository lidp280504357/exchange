---
title: Transfers between accounts
category: funds
order: 3
summary: Moving funds between the spot and futures accounts is instant and free; unrealized profit in the futures account cannot be moved out.
---

Your funds sit in two accounts:

| Account | What it is for |
|:--|:--|
:::test
| Spot | Deposits, withdrawals and spot trading; the sign-up simulated funds land here |
:::
:::formal
| Spot | Deposits, withdrawals and spot trading |
:::
| Futures | Margin for perpetual futures, in USDT |

## How to transfer

1. Open [Transfer](/assets/transfer).
2. Choose the direction (spot → futures or futures → spot) and the coin, enter an amount or click "Max".
3. Confirm: it arrives at once, free of charge, and shows in your [history](/assets/history).

## Limits on moving funds out of futures

What the futures account can transfer out excludes what positions use:

- margin reserved for open orders and held by positions cannot be moved;
- **unrealized profit** of cross positions cannot be moved until you close and realize it;
- **unrealized losses** of cross positions reduce what can be moved.

So with cross positions open, the transferable amount may be well below the balance. The page's "transferable" figure already follows these rules.

## Questions

### It says the balance is insufficient?

A transfer cannot exceed the available balance (for futures, the transferable amount). Funds held by open orders need those orders canceled first.

### The futures account cannot transfer out right now?

Cross profit and loss need a fresh mark price; while it is unavailable, transfers out fail. Try again shortly.
