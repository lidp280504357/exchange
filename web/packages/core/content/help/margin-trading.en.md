---
title: Margin trading basics
category: trading
order: 3
summary: Cross and isolated accounts, leverage and what you may borrow, hourly interest, the margin level with its warning and liquidation lines, auto-borrow and auto-repay.
---

Margin trading lets you trade spot pairs with borrowed coins: move assets from your spot account into a margin account as collateral, borrow from the platform, and trade with both. Gains and losses are both magnified, loans charge interest by the hour, and the system liquidates an account whose margin level falls too low.

## Cross and isolated

| | Cross | Isolated |
|:--|:--|:--|
| Accounts | One account | One account per pair |
| Assets | Every collateral asset covers the others | Only the pair's two coins |
| Leverage | 3x by default | 3x, 5x or 10x by pair |
| Risk | A loss on one position weighs on the whole cross account | A loss stays within that pair's account |

The "10x"-style tag beside a pair in the market list is the highest isolated leverage it takes. An isolated account opens with your first transfer into it.

## Borrowing and what you may borrow

Choose Borrow on the [margin accounts](/assets/margin) page or above the order form, then the account and the coin. What you may borrow is the smallest of:

- net assets × (leverage − 1) − what is already borrowed;
- what the platform still lends of the coin;
- the limit per user;
- what keeps the margin level at or above the warning line after the loan.

The maximum the page shows can always be borrowed, and it says which bound limits it.

## Interest

- Interest is charged by the hour: the first hour when you borrow, then one hour at every full hour on the principal still owed. Interest = principal × hourly rate.
- Interest does not compound: interest owed is not charged interest.
- Rates are fixed or floating, by coin; a floating rate moves with how much of the coin the platform has lent. The current rates are under "Borrowing rates" on the margin accounts page.
- Repayments go to interest first, then to principal; repay part or all at any time. Max repays everything, with the interest up to the repayment.

## Margin level

Margin level = total assets ÷ total liabilities, both in USDT:

- total assets value each coin at its reference price times its collateral ratio (USDT 1.00, BTC and ETH 0.95, other major coins 0.90, the platform coin ASTRA 0.70), so that volatile coins are not overvalued;
- total liabilities = principal borrowed + interest owed;
- without debts the margin level shows as 999.

| Account | Warning line | Liquidation line |
|:--|--:|--:|
| Cross 3x | 1.30 | 1.10 |
| Isolated 3x | 1.25 | 1.15 |
| Isolated 5x | 1.20 | 1.10 |
| Isolated 10x | 1.10 | 1.05 |

These are the defaults; the platform may change them, and the margin accounts page shows the ones that apply.

- When the margin level falls below the warning line you get a notice and an email (once, until it is back above the line). Move assets in, repay or reduce your position to raise it.
- When it reaches the liquidation line, the system liquidates the account.

## Liquidation

1. The margin account is frozen: no orders, loans or transfers, and its open orders are cancelled.
2. Its assets are sold at market for the coins it owes, and the loans and interest are repaid.
3. A liquidation fee of 2% of the value traded is charged.
4. What is left stays in the account, which is free again; debts the assets could not cover are paid by the platform's insurance fund.

You are notified when a liquidation starts and when it completes.

## Trading on a margin account

Above the order form on the [trading page](/trade/BTC-USDT), choose Cross or Isolated to trade from that margin account; the form then shows the leverage, the margin level and what you may borrow. There are three ways to borrow and repay:

- **Normal**: only the coins the account holds;
- **Auto-borrow**: borrows what the available balance lacks (interest starts with the loan);
- **Auto-repay**: what the fills bring in repays the loan of that coin.

The open orders mark orders on margin accounts (cross or isolated).

## Transfers

- In: from your spot account to a margin account, at once and free of fees.
- Out: only what open orders and the coin's own debt do not hold; while the account owes anything, only what keeps the margin level at or above the warning line.

## Risk warning

Leverage magnifies gains and losses alike. In a sharp move a liquidation can come within minutes and take all your collateral. Only trade with money you can afford to lose.
