---
title: How to withdraw
category: funds
order: 2
summary: Add the address to your address book and wait out its cooling period, then pick the network, enter the amount and pass a security check. Some withdrawals are reviewed by a person.
---

## What you can withdraw today

| Coin | Network | Minimum | Fee | Confirmations |
|:--|:--|--:|--:|--:|
| ETH | Sepolia (Ethereum test network) | 0.001 ETH | 0.0002 ETH | 12 |

The amount is what the recipient gets; the fee comes out of your balance on top of it, and both are held when you submit.

## Step 1: add the address to your address book

Withdrawals only go to addresses in your address book.

1. Open [Withdraw](/assets/withdraw), choose "New address", pick the network and enter the address. The page checks its format first, and the checksum of an Ethereum address written in mixed case.
2. Pass a security check and save it.
3. A new address has a **cooling period** before it can be used: 24 hours in production, shortened to 1 minute in the test environment.

Your own deposit address cannot be added. Make sure the address belongs to the person you mean to pay and that their wallet supports the network.

## Step 2: request the withdrawal

1. Choose the coin and the network, then an address from your book.
2. Enter the amount. The page shows the fee, the amount received, and what is left of today's and this month's limits.
3. Check the summary, pass a security check (with the authenticator, if one is bound) and submit.

## Limits

Limits are counted in USDT. Each day's first withdrawal fixes the prices used for the rest of that day.

| Account | Per day | Per month |
|:--|--:|--:|
| Email, phone number and authenticator all bound | 2,000 USDT | 20,000 USDT |
| Any other account | 400 USDT | 4,000 USDT |

## When a person reviews it

A withdrawal waits "Under review" until an administrator approves it when any of these hold:

- the account is less than 72 hours old;
- it comes from a device within 24 hours of its first sign-in;
- the password was changed or reset, or an identity replaced, in the last 24 hours;
- the address joined the address book less than 72 hours ago;
- it is over 1,000 USDT, or today's total passes half the daily limit.

## Statuses

The withdrawal history shows each step: under review (when needed) → approved → signing → broadcast → confirming → completed.

- **Cancel**: possible until it reaches signing; the held funds return to your available balance.
- **Rejected**: the held funds return as well.
- **Failed**: one that fails before it is sent is returned automatically; one that fails on chain is handled by the platform by hand.
- **Internal transfers**: when the address is another user's deposit address, nothing goes on chain; after approval it completes inside the site, with no fee.

You get an inbox message and an email when a withdrawal is submitted, completed, rejected or failed.

> This site is a test environment for learning; every balance is simulated.
