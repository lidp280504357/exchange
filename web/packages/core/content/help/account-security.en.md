---
title: Account security
category: account
order: 2
summary: Security checks, authenticator apps, the anti-phishing code, a second identity and your devices.
---

Every security setting is on the [Account security](/account/security) page. We suggest going through the steps below after signing up.

## Security checks

Before a withdrawal, adding a withdrawal address, changing the password, binding or replacing your email or phone, setting the anti-phishing code, binding or removing an authenticator, or signing out other devices, you pass a **security check**:

- with an authenticator bound: the 6-digit code from the app (the only way accepted);
- without one: a code sent to your bound email or phone, preferably the identity you did not sign in with.

Each check covers the one action that follows it and expires after 10 minutes if unused.

## Authenticator (TOTP)

An authenticator is the strongest protection: even with your mailbox stolen, nobody can withdraw without the app on your phone.

1. On the security page, choose to bind an authenticator and pass a security check.
2. Scan the QR code with an app such as Google Authenticator, Microsoft Authenticator or 1Password, or type in the secret.
3. Enter the 6-digit code the app shows.

Keep in mind:

- Back up the secret or turn on the app's backup. There are no recovery codes yet: if you lose the app, an administrator has to verify you by hand before removing it.
- Each code works once. If a code is refused, wait for the next one and check that your phone's clock is right.

## Anti-phishing code

The anti-phishing code is 4–20 letters or digits you choose. It appears **at the top of every security email** we send you. When an email claims to be from Astras, look for the code first: if it is missing or wrong, the email is fake, so do not click its links.

Setting or clearing the code takes a security check.

## A second identity

An account can have one email and one phone number:

- Each backs up the other: when you forget the password or replace one of them, the other one confirms it is you.
- Accounts with an email, a phone number and an authenticator can withdraw up to 2,000 USDT a day and 20,000 USDT a month (equivalent); other accounts get 20% of that.

With two identities bound, replacing one needs a check through the other; an account with a single identity sends the change for review by a person. Withdrawals in the 24 hours after a change are reviewed by a person.
:::test
SMS is simulated in the test environment, so a phone number receives no real text messages.
:::

## Changing the password

Changing the password takes the current password and a security check. Every other device is signed out, and withdrawals in the next 24 hours are reviewed by a person.

## Devices and sign-ins

[Devices](/account/sessions) lists the devices signed in to your account and your recent sign-ins:

- the device you are using is marked;
- sign out one you do not recognize, or all but this one at once (after a security check);
- withdrawals from a device in its first 24 hours are reviewed by a person.

## Security notices

These send both an inbox message and an email: a sign-in from a new device, a password change, an identity bound or replaced, a locked account, a change of account status, and a new authenticator. If it was not you, change your password and sign out the other devices right away.
