---
title: FAQ
category: faq
order: 1
summary: Answers about funds and accounts, refused orders, codes, account statuses and the desktop and mobile sites.
---

## Funds and accounts

:::test
### Is the money real?

No. The site is in test mode and every balance is **simulated**: it cannot be exchanged for real assets or withdrawn as real money. Deposits and withdrawals only use the Ethereum Sepolia test network, whose ETH has no value either. See [About the test environment and simulated funds](/announcements/test-environment).

### Where do the simulated funds come from?

Every new account receives simulated funds in its spot account once, when it signs up; the sign-up page shows how much. There is no way to request more yourself for now.
:::

### What do "under review" and "frozen" mean on my account?

| Status | What you can do |
|:--|:--|
| Active | Everything |
| Under review | Sign in, look around and trade spot; futures, transfers and withdrawals wait for the risk review |
| Frozen | Sign in and look only: no trading, transfers or withdrawals; deposits are still credited |
| Closed | Nothing: sign-in is refused |

## Trading

### Why was my order refused?

The usual reasons and what to do:

- **Insufficient balance**: check your available balance; open orders hold funds, so cancel the ones you no longer need;
- **Price or amount off the steps**: use the pair's price step and amount step;
- **Below the minimum value**: at least 5 USDT per order on USDT pairs;
- **Price too far from the last trade**: a limit price far from the market is refused; adjust it and try again;
- **The pair is not taking orders**: it is coming soon, halted or cancel only;
- **A post-only order would fill at once**: use a plain limit order or change the price.

### Why has my limit order not filled?

A limit buy fills only when someone sells at your price or lower, and the other way round for a sell. Far from the market, that can take a long time: cancel and place it closer to the market price, or use a market order.

### How are fees worked out?

Spot makers and takers pay 0.10%, buyers in the base coin and sellers in the quote coin; futures makers pay 0.02% and takers 0.05%, in USDT. See [Fees](/help/fees).

### Are prices live?

Yes. Prices, candles and 24-hour figures follow the major markets live over one connection. When the top of the page says it is reconnecting, the network dropped for a moment; it reconnects and catches up by itself.

## Signing in and security

### Why does no code arrive?

- The email may be in your spam folder;
- one code per email every 60 seconds, at most 5 an hour and 10 a day;
:::test
- SMS is simulated in the test environment and phone numbers get no real messages: use email.
:::

### Why am I asked for a code when signing in?

After 7 days without a successful sign-in, the correct password must be followed by a code sent to your bound email, so a leaked password alone cannot open the account. See [Signing up and signing in](/help/register-login).

### Why is my withdrawal under review?

New accounts, new devices, recent password or identity changes, addresses new to your address book and large amounts are all reviewed by a person. See [How to withdraw](/help/withdraw).

## The website

### How do the desktop and mobile sites differ?

They do the same things, laid out for wide screens and for phones held upright: the desktop site is astras.vip, the mobile site m.astras.vip. Phones opening the desktop site go to the same page on the mobile site, and the footer switch remembers your choice. Each site keeps its own sign-in.

### How do I change the language, time zone or colors?

Open [Settings](/account/settings): choose Chinese or English, the time zone for times on screen, the colors of rises and falls (green up or red up), and whether orders ask for confirmation. The top bar also switches the language.

### Is there an API?

Yes. The public REST and WebSocket reference is at [astras.vip/docs](/docs/), the same interfaces the website uses.
