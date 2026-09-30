---
title: Authenticator apps (TOTP) are supported
date: 2026-09-29
category: security
summary: Bind an authenticator app on the account security page and confirm withdrawals, password changes and other sensitive actions with its 6-digit codes.
---

To make sensitive actions safer, Astras now supports authenticator apps (TOTP, time-based one-time passwords).

## What changes once it is bound

Withdrawals, adding a withdrawal address, changing the password, binding or replacing your email or phone, setting the anti-phishing code and signing out other devices all start with a **security check**. With an authenticator bound, the check takes the 6-digit code from the app instead of an email or SMS code, so a stolen mailbox or phone number is no longer enough.

Any app that follows RFC 6238 works, for example Google Authenticator, Microsoft Authenticator or 1Password: a new 6-digit code every 30 seconds.

## How to bind one

1. Sign in and open [Account security](/account/security), then find "Authenticator".
2. Pass a security check first: a code goes to your bound email or phone.
3. Scan the QR code with the app, or type in the secret shown on the page.
4. Enter the 6-digit code the app shows. It is bound, and a security notice arrives by email.

## Good to know

- Once bound, security checks **only accept** authenticator codes, and each code works once.
- Back up the secret or turn on the app's own backup. There are no recovery codes yet: if you lose the authenticator, an administrator has to verify you by hand before removing it.
- Removing the authenticator also takes a security check with it.
- Accounts with an email, a phone number and an authenticator can withdraw up to 2,000 USDT a day and 20,000 USDT a month (equivalent); other accounts get 20% of that.

See [Account security](/help/account-security) in the help center for more.

> This site is a test environment for learning; every balance is simulated.
