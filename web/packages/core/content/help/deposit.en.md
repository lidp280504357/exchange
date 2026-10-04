---
title: How to deposit
category: funds
order: 1
summary: Pick a coin and a network, send to your own address, and the deposit is credited after enough confirmations. Deposits on the wrong network or below the minimum are not credited automatically.
---

## What you can deposit today

:::test
The test environment accepts one kind of deposit for now:

| Coin | Network | Confirmations | Usual time | Minimum |
|:--|:--|--:|--:|--:|
| ETH | Sepolia (Ethereum test network) | 12 | about 3 minutes | 0.001 ETH |

Other coins trade on the site only and cannot be deposited or withdrawn. Sepolia ETH has no market value; public test-network faucets give it away for free.
:::
:::formal
The coins and networks you can deposit are the ones the [Deposit](/assets/deposit) page lists, each with its confirmations, minimum deposit and usual time. Coins not on that list trade on the site only and cannot be deposited or withdrawn.
:::

## Steps

1. Sign in and open [Deposit](/assets/deposit).
2. Choose the coin, then the network. Each network shows its confirmations, minimum deposit and usual time.
3. Copy the deposit address, or scan its QR code with your wallet, and send from your wallet to that address.
4. Once the transfer is in a block, the deposit shows as confirming with its count (for example 3/12). At the full count it is credited to your spot account and you are notified.

Your deposit address is yours alone and can be used again and again; every coin on the same network shares it.

## Before you send

> **Only send assets on this network to this address.** Transfers on another network, or of tokens the site does not support, are not credited automatically and may not be recoverable.

:::test
- **Use the same network**: the site only watches Sepolia. Assets sent to the same address on Ethereum mainnet or another network are invisible to it and never credited.
:::
:::formal
- **Use the same network**: send on the network you chose on the deposit page. Assets sent to the same address on another network are invisible to the site and never credited.
:::
- **Send at least the minimum**: a deposit below the minimum is not credited to your account; it is held for manual handling, and you are told.
- **Use a plain transfer**: transfers made inside a contract (by some smart-contract wallets, for example) may not be recognized automatically and have to be added by hand.

## Questions

### How long does a deposit take?

:::test
It depends on the blocks. Sepolia makes a block about every 12 seconds, so 12 confirmations usually take around 3 minutes, longer when the network is busy.
:::
:::formal
It depends on the network's block time and the confirmations it needs (the deposit page shows the usual time), longer when the network is busy.
:::

### The confirmations are complete but my balance did not change?

Check the deposit's status first. "Rejected" usually means it was below the minimum or of an unsupported token: such deposits are not credited automatically, only the platform can handle them by hand, and recovery is not guaranteed.

### Can a frozen account receive deposits?

Yes, they are credited, but the funds cannot be used or withdrawn while the account is frozen.
