// Generated from api/admin/admin.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/admin/v1/login-options": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * What signing in asks for
         * @description Whether the sign-in page asks for the authenticator code: true
         *     unless the feature flag admin.login_without_totp is on. Needs no
         *     session.
         */
        get: operations["loginOptions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/login": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Sign in with password and authenticator code
         * @description Sets the session cookie. Fails with ADMIN_LOGIN_FAILED for a wrong
         *     email, password or code (or a code used before), ADMIN_LOCKED
         *     after five failures in a row (15 minutes), and COMMON_RATE_LIMITED
         *     beyond 10 attempts a minute from one IP. The code may be left out
         *     while the flag admin.login_without_totp is on; it is then not
         *     checked.
         */
        post: operations["login"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/logout": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** End the session */
        post: operations["logout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/me": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The signed-in administrator and their permissions */
        get: operations["me"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/me/password": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Change one's own password
         * @description The current password proves it is them (ADMIN_PASSWORD_WRONG); the
         *     new one has at least 12 characters. Their other sessions end.
         *     Audited as admin.password_changed. Open to an administrator who
         *     must change their password first (must_change_password); nothing
         *     else is (ADMIN_PASSWORD_CHANGE_REQUIRED, C5.5 ⑪).
         */
        post: operations["changeOwnPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/me/totp/start": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * A new authenticator for oneself, to bind
         * @description The current password and, while sign-in asks for codes, the current
         *     authenticator's code prove it is them (ADMIN_PASSWORD_WRONG,
         *     ADMIN_TOTP_CODE_WRONG). Answers with a new secret and its otpauth
         *     URI (no-store); the old authenticator signs in until POST
         *     /admin/v1/me/totp binds the new one, within 10 minutes. This and
         *     the other two of one's own credentials take 10 requests in 15
         *     minutes per administrator (COMMON_RATE_LIMITED).
         */
        post: operations["startOwnTOTP"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/me/totp": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Bind the new authenticator with its code
         * @description A wrong code is ADMIN_TOTP_CODE_WRONG; nothing waiting (or late)
         *     COMMON_CONFLICT. Their other sessions end. Audited as
         *     admin.totp_changed.
         */
        post: operations["confirmOwnTOTP"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/setup/inspect": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * What a one-time setup link sets up
         * @description No session: the token is the proof (ADMIN_SETUP_INVALID when it is
         *     unknown, used or expired). Answers with the account and, when the
         *     link binds an authenticator, its secret and otpauth URI (no-store).
         *     Throttled with the sign-in.
         */
        post: operations["inspectAdminSetup"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/setup": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Set what a one-time setup link sets up
         * @description The password (at least 12 characters) when the link sets one, the
         *     authenticator's current code when it binds one
         *     (ADMIN_TOTP_CODE_WRONG). The link is spent; the administrator then
         *     signs in as usual. Audited as admin.setup_completed in their name,
         *     with the address it came from. Throttled with the sign-in.
         */
        post: operations["completeAdminSetup"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/roles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Every role with its permissions
         * @description Any administrator reads them.
         */
        get: operations["listRoles"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/admins": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The administrators
         * @description Every administrator, with their live sessions. Needs admins.manage.
         */
        get: operations["listAdmins"];
        put?: never;
        /**
         * Create an administrator
         * @description Answers with the administrator and a one-time setup link's token
         *     (a day), shown this once: the answer is not cached (Cache-Control:
         *     no-store), and the token is neither logged nor audited. Its holder
         *     sets the password and binds the authenticator (POST
         *     /admin/v1/setup): whoever created the account never knows what
         *     signs it in (C5.5 ⑪). Audited as admin.created. Needs
         *     admins.manage.
         */
        post: operations["createAdmin"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/admins/{id}/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Disable or enable an administrator
         * @description Disabling ends their sessions at once; enabling also lifts a lock
         *     after failed sign-ins. Nobody changes their own account here
         *     (ADMIN_SELF), and an active ADMIN always remains
         *     (ADMIN_LAST_ADMIN). Audited as admin.disabled or admin.enabled.
         *     Needs admins.manage.
         */
        post: operations["setAdminStatus"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/admins/{id}/role": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Change an administrator's role
         * @description Takes effect on their next request. Not one's own (ADMIN_SELF), nor
         *     the last active ADMIN's (ADMIN_LAST_ADMIN). Audited as
         *     admin.role_changed. Needs admins.manage.
         */
        post: operations["setAdminRole"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/admins/{id}/password-reset": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Reset an administrator's password
         * @description The old password stops working and their sessions end; answers
         *     with a one-time setup link's token (a day, no-store, never logged
         *     or audited) whose holder sets a new one (C5.5 ⑪). Not one's own
         *     (ADMIN_SELF). Audited as admin.password_reset. Needs admins.manage.
         */
        post: operations["resetAdminPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/admins/{id}/totp-reset": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Reset an administrator's authenticator
         * @description The old authenticator stops working and their sessions end;
         *     answers with a one-time setup link's token (a day, no-store, never
         *     logged or audited) whose holder binds a new one (C5.5 ⑪). Not
         *     one's own (ADMIN_SELF). Audited as admin.totp_reset. Needs
         *     admins.manage.
         */
        post: operations["resetAdminTOTP"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/admins/{id}/sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An administrator's live sessions
         * @description At most 50, the latest first. Needs admins.manage.
         */
        get: operations["adminSessions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/admins/{id}/sessions/revoke": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * End every session of an administrator
         * @description Not one's own (ADMIN_SELF; signing out ends one's session).
         *     Audited as admin.sessions_revoked. Needs admins.manage.
         */
        post: operations["revokeAdminSessions"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/settings": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The console's settings
         * @description Whether fund operations need a second administrator (the flag
         *     admin.two_person_approval) and the single-person limits, with the
         *     caller's single-person total of the last 24 hours. Every
         *     administrator may read them.
         */
        get: operations["getSettings"];
        /**
         * Change the console's settings
         * @description Changes the fields given: the limits and the delay of trading
         *     parameters' changes (audited as admin.settings.changed) and
         *     two-person approval (switches the flag
         *     admin.two_person_approval, audited by the flags; the other
         *     instances follow within 5 seconds). The daily limit must cover the
         *     single-operation one, and each limit is at most ten times its
         *     default (single 1,000,000, 24 hours 5,000,000, withdrawal
         *     1,000,000 USDT). Needs settings.write (ADMIN).
         */
        put: operations["updateSettings"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/todo": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * What waits for the administrator
         * @description The counts behind the console's badges, as far as the role shows
         *     them (zero otherwise): withdrawals in review (counted up to 200)
         *     and fund operations waiting for a decision. A count that cannot be
         *     read is zero and named in `partial`.
         */
        get: operations["getTodo"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The counts as they change (Server-Sent Events)
         * @description An event stream: `todo` with the counts of GET /admin/v1/todo at
         *     once and whenever they change (checked every 10 seconds),
         *     `signed_out` when the session ends, and a comment after 20 seconds
         *     without an event. The stream does not keep the session alive; the
         *     browser reconnects after 5 seconds when it drops.
         */
        get: operations["streamEvents"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Accounts, newest first
         * @description Needs users.read. Contact data stays in auth-service; find an account by email or phone with users/lookup.
         */
        get: operations["listUsers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/orders": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Spot orders in their latest state, newest first
         * @description From the read model orders_current. Needs reports.read.
         */
        get: operations["listOrders"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/trades": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Spot trades, newest first
         * @description From the read model trades; user_id matches either side. With accounts=bots only the trades between two bots, with accounts=users those with anyone else on a side (a user's trade with a bot is the users'). Needs reports.read.
         */
        get: operations["listTrades"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/deposits": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Deposits in their latest state, newest first
         * @description From the read model wallet_deposits. Needs withdrawals.read.
         */
        get: operations["listDeposits"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/deposits/review": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * wallet-service's deposits, for decisions
         * @description Newest first, straight from wallet-service (not the read model):
         *     attention=true lists the deposits waiting for a decision (booked
         *     to UNCLAIMED_DEPOSIT, or a backfill the custodian's callback
         *     disagreed with), manual_pending=true the backfilled ones without
         *     a callback yet. Needs withdrawals.read.
         */
        get: operations["listReviewDeposits"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/deposits/manual/check": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Check a backfill of a lost custodian callback without booking it
         * @description The checks of POST /admin/v1/deposits/manual (the network is a
         *     custodian's, the address is a user's on it, neither the trade nor
         *     the transfer is known, the amount fits the asset's precision) and
         *     what it would book. Not audited. Needs deposits.review.
         */
        post: operations["checkDepositBackfill"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/deposits/manual": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Backfill a custodian deposit whose callback was lost
         * @description For a deposit the administrator found in the custodian's console
         *     (this console cannot ask the custodian, so nothing here checks it
         *     there). After the checks of POST /admin/v1/deposits/manual/check
         *     it is a fund operation (kind DEPOSIT_BACKFILL): in single-person
         *     mode within the limits it is booked at once (EXECUTED), otherwise
         *     it waits for a second administrator (PENDING). Booked like a
         *     callback (credited, or to UNCLAIMED_DEPOSIT below the minimum)
         *     with source MANUAL; the custodian's own callback, when it comes,
         *     is matched against it, never booked again. Audited. Needs
         *     deposits.review.
         */
        post: operations["backfillDeposit"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/deposits/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * One of wallet-service's deposits
         * @description Needs withdrawals.read.
         */
        get: operations["getReviewDeposit"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/deposits/{id}/credit": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Give an unclaimed deposit to its user
         * @description Only a deposit booked to UNCLAIMED_DEPOSIT (below the minimum, the
         *     account closed or not eligible) and only its own asset and amount:
         *     the ledger moves it to the user's spot account (DEPOSIT_CREDIT,
         *     audited as ledger.unclaimed_released). An unsupported token has no
         *     asset and can only be rejected (WALLET_DEPOSIT_NOT_RELEASABLE).
         *     Needs deposits.review.
         */
        post: operations["creditDeposit"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/deposits/{id}/assign": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Credit a deposit of nobody to a user
         * @description A deposit of nobody (user_id the nil UUID, reason UNKNOWN_ADDRESS:
         *     the custodian reported a deposit to an address no user has, B7a)
         *     waits in UNCLAIMED_DEPOSIT. This credits its own asset and amount
         *     to the user named: a fund operation of kind DEPOSIT_ASSIGN, carried
         *     out at once in single-person mode within the limits, else waiting
         *     for a second administrator (C5.5 ㉑). Credited to a user other than
         *     the address's holder, now or before it was retired, it waits for a
         *     second administrator whatever its worth (escalation
         *     NOT_ADDRESS_HOLDER). wallet-service sets the owner
         *     and releases it (audited as wallet.deposit.assigned and
         *     ledger.unclaimed_released). Any other deposit is
         *     ADMIN_DEPOSIT_NOT_UNOWNED; while another request for the deposit
         *     waits or once one was carried out, ADMIN_DEPOSIT_ASSIGN_OPEN (one
         *     live request per deposit); a user who may not take deposits is
         *     refused by wallet-service. The same request under its
         *     Idempotency-Key returns the operation, finishing one whose outcome
         *     was unknown. Needs deposits.review.
         */
        post: operations["assignDeposit"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/deposits/{id}/reject": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Close a deposit waiting for a decision without moving funds
         * @description Marks it handled with the reason (resolution DISMISSED): an
         *     unclaimed deposit stays in UNCLAIMED_DEPOSIT, a disagreeing
         *     callback stays unbooked (a backfill it disagreed with is never
         *     sent to the ledger). An unclaimed deposit the ledger released
         *     already, its release not recorded, is not closed: 409
         *     WALLET_DEPOSIT_RELEASED (details journal_id); crediting it again
         *     records the release. A deposit of nobody the ledger released to a
         *     user already is not closed either: 409
         *     WALLET_DEPOSIT_RELEASED_TO_USER (details user_id, journal_id);
         *     assigning it to that user records the release. Audited by the
         *     wallet (wallet.deposit.dismissed). Needs deposits.review.
         */
        post: operations["rejectDeposit"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/dashboard": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The overview
         * @description Accounts (user-service), the last 24 hours' trading and what waits
         *     in the wallet (read models), the reference feed (market-data-service)
         *     and a daily series. A part that cannot be read is left empty and
         *     named in `partial`. Needs reports.read.
         */
        get: operations["getDashboard"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/lookup": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Find an account by user ID, email address or phone number
         * @description Phone numbers in E.164 with the leading "+". Needs users.read.
         */
        get: operations["lookupUser"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Move an account to another status (freeze, unfreeze, review, close)
         * @description user-service applies its state machine (appendix B) and records the
         *     change with the administrator as the actor. Needs users.status.
         */
        post: operations["changeUserStatus"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An account with the console's tags on it
         * @description Needs users.read. Unknown accounts fail with COMMON_NOT_FOUND.
         */
        get: operations["getUser"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/notes": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Administrators' notes on an account, newest first
         * @description Needs users.read.
         */
        get: operations["listUserNotes"];
        put?: never;
        /**
         * Write a note on an account
         * @description Notes are never edited or removed; each is audited (admin.users.note_added). Needs users.notes.
         */
        post: operations["addUserNote"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/tags": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Replace an account's tags
         * @description Tags are upper case codes (letters, digits, _; up to 32, at most
         *     10), e.g. VIP, SUSPICIOUS, TEST; the console's own, user-service
         *     never sees them. A change is audited with the tags before and after
         *     (admin.users.tags_changed). Needs users.notes.
         */
        put: operations["setUserTags"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/adjustments": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Credit or debit a user's balance
         * @description A manual adjustment (MANUAL_ADJUSTMENT against the ADJUSTMENT system
         *     account) of the user's SPOT or FUTURES account: booked at once in
         *     single-person mode within the limits (EXECUTED with its journal, or
         *     FAILED with the ledger's refusal such as
         *     LEDGER_ADJUSTMENT_DISABLED), otherwise PENDING for a second
         *     administrator with the reason in `escalation`. When the ledger does
         *     not answer the call fails with COMMON_UNAVAILABLE and the detail
         *     `approval_id` names the operation, left PENDING for its requester
         *     to finish (decide). Needs ledger.adjust.request.
         */
        post: operations["adjustUserBalance"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/cancel-orders": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel every open order of an account
         * @description Needs orders.cancel. The cancellations complete asynchronously in the matching engine.
         */
        post: operations["cancelUserOrders"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/security": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An account's sign-in security
         * @description Identities (masked: a***@x.com, +65912****4567), authenticator,
         *     password, lock, live sessions and devices (addresses masked), and
         *     the rebind requests waiting (auth-service). Needs users.read.
         */
        get: operations["getUserSecurity"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/contacts/reveal": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * An account's email and phone, unmasked
         * @description Each call is audited (admin.users.contacts_revealed, naming the
         *     kinds shown, not the values); the answer is not cached. Needs
         *     users.contacts.
         */
        post: operations["revealUserContacts"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/login-history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An account's sign-in attempts, newest first
         * @description Addresses are masked. Needs users.read.
         */
        get: operations["listUserLogins"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/sessions/revoke": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * End one of an account's sessions, or all of them
         * @description The sessions end with reason ADMIN (auth-service); audited as
         *     admin.users.sessions_revoked. Needs users.security.
         */
        post: operations["revokeUserSessions"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/totp-reset": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Remove an account's authenticator app
         * @description The user is told by mail and can bind a new one; audited as
         *     admin.users.totp_reset. Needs users.security.
         */
        post: operations["resetUserTotp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/password-reset": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Give an account a temporary password
         * @description A random password (four groups of four letters and digits) replaces
         *     the user's; every session ends, the password lock is cleared and
         *     the user is told by mail as for a reset (withdrawals are reviewed
         *     for a day). The password is in this answer only: pass it on and
         *     ask the user to change it. Audited as admin.users.password_reset,
         *     without the password. Needs users.security.
         */
        post: operations["resetUserPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An account's status changes and accepted documents
         * @description Status changes newest first, at most 50 (user-service). Needs users.read.
         */
        get: operations["getUserHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/risk": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The risk rules' assessments of an account, newest first
         * @description Only assessments with a rule hit are kept (risk-service). Moving the
         *     account to RISK_REVIEW and back is POST /admin/v1/users/{id}/status.
         *     Needs users.read.
         */
        get: operations["getUserRisk"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/identity-requests": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Identity rebind requests, newest first
         * @description A user with a single identity asks to move it to a new value and an
         *     administrator decides (auth-service). Values are masked. Needs
         *     users.read.
         */
        get: operations["listIdentityRequests"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/identity-requests/{id}/decide": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve or reject an identity rebind request
         * @description Approving moves the identity to the new value and tells the user;
         *     a value another account holds fails with AUTH_IDENTITY_TAKEN. Audited as
         *     admin.users.identity_request_decided. Needs users.security.
         */
        post: operations["decideIdentityRequest"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/balances": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An account's balances, valued in USDT
         * @description Every SPOT and FUTURES balance with its total (available + frozen)
         *     valued at its USDT pair's last price, SPOT first and the larger
         *     worth first; assets without a USDT price are listed in `unpriced`
         *     and left out of the total. Needs users.read.
         */
        get: operations["getUserBalances"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/holds": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Holds on an account's SPOT balance, newest first
         * @description Active and released. Needs users.read.
         */
        get: operations["listUserHolds"];
        put?: never;
        /**
         * Freeze part of an account's SPOT balance
         * @description The ledger moves the amount from available to frozen (ADMIN_FREEZE)
         *     and audits it as ledger.hold_placed; more than the available
         *     balance fails with LEDGER_INSUFFICIENT_BALANCE. The user sees the
         *     amount frozen until it is released. Needs ledger.hold.
         */
        post: operations["placeUserHold"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/holds/{hold}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * Release a hold
         * @description The ledger returns the amount to the available balance
         *     (ADMIN_UNFREEZE) and audits it as ledger.hold_released; a hold is
         *     released once (LEDGER_HOLD_RELEASED). Needs ledger.hold.
         */
        delete: operations["releaseUserHold"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/orders/{order}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel one of an account's spot orders
         * @description The cancel completes asynchronously in the matching engine; the
         *     answer is the order as spot-trading-service has it. Audited as
         *     admin.orders.canceled. Needs orders.cancel.
         */
        post: operations["cancelUserOrder"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/contract-orders": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An account's active contract orders
         * @description As derivatives-service renders them, at most 100. Needs users.read.
         */
        get: operations["listUserContractOrders"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/contract-orders/{order}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel one of an account's contract orders
         * @description Completes asynchronously in the engine. Audited as admin.derivatives.order_canceled. Needs orders.cancel.
         */
        post: operations["cancelUserContractOrder"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/positions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An account's open contract positions
         * @description As derivatives-service renders them, with the liquidation estimate. Needs users.read.
         */
        get: operations["listUserPositions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/futures-margin": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An account's cross margin, and what a debit would leave of it
         * @description The cross positions' equity and maintenance margin at the marks, as
         *     derivatives-service's margin monitor measures them, and the equity
         *     and state after a debit of the FUTURES balance (a negative
         *     adjustment lowers the equity at once; LIQUIDATE means the next round
         *     of the monitor takes the positions over). Unmeasured while a cross
         *     position's contract has no fresh mark price. Needs users.read.
         */
        get: operations["getUserFuturesMargin"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/users/{id}/positions/close": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Close a position at the market (force close)
         * @description The user's orders resting on the contract are canceled first, all
         *     of them (an opening order filled after the close would open the
         *     position again); once the engine confirmed (the console waits a few
         *     seconds, then fails with DERIV_CLOSE_PENDING to try again), a
         *     market order of kind ADMIN takes the whole position: reduce-only in
         *     one-way mode, against its side in hedge mode. A position under
         *     liquidation is left to the liquidation engine
         *     (DERIV_POSITION_LIQUIDATING); HOUSE's are not closed here
         *     (DERIV_HOUSE_NOT_CLOSED). The answer is the order as last seen: the
         *     console waits a few seconds for it to finish. On a thin book it may
         *     fill in part (CANCELED with filled_quantity below quantity): the
         *     rest of the position stays, look again and close it again. Audited
         *     as admin.derivatives.position_close_requested with its key, and as
         *     admin.derivatives.position_closed with what filled once the order is
         *     final. Needs derivatives.write.
         */
        post: operations["closeUserPosition"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/withdrawals": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Withdrawals, the review queue by default
         * @description From wallet-service. The review queue (PENDING_REVIEW) lists oldest
         *     first, every other status newest first, unless order says otherwise.
         *     Needs withdrawals.read.
         */
        get: operations["listWithdrawals"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/withdrawals/{id}/review": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve or reject a withdrawal in review
         * @description The administrator's email is the reviewer; a withdrawal above
         *     20,000 USDT needs two distinct reviewers. In single-person mode
         *     one approval completes a withdrawal worth at most the settings'
         *     withdrawal_max_usdt both when it was requested and at the current
         *     price; one worth more now, or of no fresh price (over a minute
         *     old), needs two reviewers whatever the risk rules asked for: the
         *     approval counts and another administrator approves too. Rejecting
         *     releases the frozen amount. The same review again with its
         *     Idempotency-Key answers with the withdrawal as it stands. A
         *     withdrawal of an asset whose withdrawals are suspended is approved
         *     all the same; the answer carries suspended_at and
         *     suspension_reason: it waits until they are resumed. Needs
         *     withdrawals.review.
         */
        post: operations["reviewWithdrawal"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/withdrawals/review-batch": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve or reject several withdrawals with one reason
         * @description Each withdrawal is reviewed on its own as by POST
         *     /admin/v1/withdrawals/{id}/review (and audited by the wallet); one
         *     that fails (already decided, unknown) leaves the others decided and
         *     says why. One on hold is left out (ADMIN_WITHDRAWAL_HELD: its
         *     reviewer decides it on its own). At most 50 at a time. The same
         *     batch again with its Idempotency-Key answers for those it reviewed
         *     as they stand. Needs withdrawals.review.
         */
        post: operations["reviewWithdrawalBatch"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/withdrawals/suspensions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The assets whose withdrawals are suspended
         * @description wallet-service suspends an asset's withdrawals when funds are
         *     missing on two custody checks (or an operator does, exchangectl
         *     wallet withdrawals-suspend): new requests are refused, approved
         *     ones wait. Needs withdrawals.read.
         */
        get: operations["listWithdrawalSuspensions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/withdrawals/suspensions/{asset}/resume": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Lift an asset's withdrawal suspension
         * @description Its approved withdrawals go out within a round, and the custody
         *     checks start over (funds still missing suspend it again on two
         *     checks). wallet-service audits it (wallet.withdrawals.resume) in
         *     the ADMIN's name. COMMON_NOT_FOUND when it is not suspended.
         *     Needs withdrawals.resume (ADMIN).
         */
        post: operations["resumeWithdrawals"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/withdrawals/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A withdrawal with what its review needs
         * @description From wallet-service: the withdrawal, its address in the user's
         *     address book (when it was added, when its cooling-off ended) and
         *     the user's withdrawals' worth today and this month. Needs
         *     withdrawals.read.
         */
        get: operations["getWithdrawal"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/withdrawals/{id}/hold": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Put a withdrawal in review on hold, or take it off hold
         * @description A held withdrawal stays in review with the reviewer's note (say,
         *     while the user is called) until it is taken off hold or decided;
         *     deciding it takes it off hold. Only a withdrawal in review
         *     (WALLET_WITHDRAWAL_NOT_IN_REVIEW otherwise). Audited by the wallet
         *     (wallet.withdrawal.hold, wallet.withdrawal.unhold). Needs
         *     withdrawals.review.
         */
        post: operations["holdWithdrawal"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Assets (with networks), trading pairs and perpetual contracts
         * @description As instrument-service returns them (protobuf JSON with field names;
         *     int64 as strings). They change through the versioned reference-data
         *     file (exchangectl instruments apply, run by every deploy) and, since
         *     C3, through POST /admin/v1/instruments/apply. Needs instruments.read.
         */
        get: operations["listInstruments"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments/config": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The reference data as a config document
         * @description In the shape of deploy/instruments/<env>.json (what exchangectl
         *     instruments apply reads), statuses and versions included: what the
         *     console's forms start from. Needs instruments.read.
         */
        get: operations["getInstrumentConfig"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments/preview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * What a config document would change, changing nothing
         * @description The document names only the items to create or change, each whole
         *     (a field left out becomes empty). Items missing are left alone,
         *     statuses never change here (the status endpoints do that): a new
         *     pair or contract starts in PREPARE (ADMIN_NEW_ITEM_NOT_PREPARE for
         *     another status). A pair's
         *     new reference symbol must be listed on the reference market's spot
         *     market (ADMIN_REFERENCE_UNKNOWN otherwise: it would fail the
         *     reference reads of every pair); clearing the reference symbol of a
         *     pair HOUSE quotes or a perpetual's index follows is refused
         *     (ADMIN_REFERENCE_IN_USE); `warnings` note what else follows.
         *
         *     `guard` lists the trading parameters the document moves (fee rates,
         *     a pair's fee tier, reference symbol and multiplier, a contract's
         *     fee tier and risk ladder) with, for a ladder, the open positions it
         *     would liquidate (derivatives-service measures them as its margin
         *     monitor does). Such a document is applied only by an ADMIN bringing
         *     back `guard.confirmation` (bound to them and to exactly these
         *     changes, valid 10 minutes), and then waits (see apply). Needs
         *     instruments.write.
         */
        post: operations["previewInstrumentConfig"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments/apply": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Apply a config document
         * @description As POST /admin/v1/instruments/preview, then applied in one
         *     transaction by instrument-service (source CONSOLE in its history,
         *     each change versioned and published). The deploy's sync of the
         *     reference-data file keeps what the console changed last (exchangectl
         *     instruments apply --force overrides). Audited
         *     (admin.instruments.applied). Needs instruments.write.
         *
         *     A document moving trading parameters (design 2026-10-02 §2 item 6)
         *     needs instruments.trading (ADMIN) and the preview's confirmation
         *     (ADMIN_CONFIRMATION_REQUIRED without it, with `reason` expired or
         *     changed when it no longer holds): it answers 202 with the change,
         *     the same change when the confirmation is brought again (one
         *     confirmation confirms one change), which takes effect the settings'
         *     change_delay_seconds later, or,
         *     while admin.two_person_approval is on, that long after a second
         *     ADMIN approved it (POST /admin/v1/instruments/changes/{id}/decide);
         *     audited as admin.instruments.change_requested. New items touch
         *     nobody until opened and apply at once.
         */
        post: operations["applyInstrumentConfig"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/articles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A section's announcements or help articles, every status
         * @description Kept by notification-service and read by the sites through GET
         *     /v1/announcements and /v1/help once published (design 2026-10-02
         *     §4.5). Every administrator reads them.
         */
        get: operations["listArticles"];
        put?: never;
        /**
         * Write a draft
         * @description Chinese (zh-CN) title and Markdown body required, English
         *     optional; the slug is unique in its section
         *     (NOTIFY_ARTICLE_EXISTS). Audited as admin.content.created on
         *     <section>:<slug>. Needs content.write.
         */
        post: operations["createArticle"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/articles/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** An article with every text */
        get: operations["getArticle"];
        /**
         * Rewrite an article
         * @description At the version read (COMMON_CONFLICT when someone saved it
         *     meanwhile); its status stays: a published article changes on the
         *     sites within a minute. Audited as admin.content.updated. Needs
         *     content.write.
         */
        put: operations["updateArticle"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/articles/{id}/publish": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Publish an article, now or at a time
         * @description The sites show it from publish_at on (now when left out). Audited
         *     as admin.content.published. Needs content.write.
         */
        post: operations["publishArticle"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/articles/{id}/archive": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Take an article off the sites
         * @description It may be published again. Audited as admin.content.archived. Needs content.write.
         */
        post: operations["archiveArticle"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/broadcasts": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The in-app messages sent, newest first
         * @description With how many each reached and how many of them read it. Every administrator reads them.
         */
        get: operations["listBroadcasts"];
        put?: never;
        /**
         * Send an in-app message to one user, a tag's users or everyone
         * @description notification-service delivers it in rounds, each user once in their
         *     language (Chinese without English), as a notice of type BROADCAST
         *     on the sites' inbox and pushed live; email also mails it. A tag's
         *     users are those tagged now (ADMIN_TAG_EMPTY when none, at most
         *     10,000). Audited as admin.notices.sent on broadcast:<id>. Needs
         *     notices.send.
         */
        post: operations["sendBroadcast"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/broadcasts/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** An in-app message sent, with its counts */
        get: operations["getBroadcast"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/broadcasts/{id}/resume": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Send a FAILED in-app message again from where it stopped
         * @description A message whose rounds failed ten times in a row (about half an
         *     hour, the error in last_error) waits for this; the other messages
         *     went on meanwhile (C5.5 ⑫). Anything but FAILED is COMMON_CONFLICT.
         *     Audited as admin.notices.resumed. Needs notices.send.
         */
        post: operations["resumeBroadcast"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The simulated market of the platform coin, as market-sim renders it
         * @description ASTRA design §6 (docs/runbook/market-sim.md): the target and the
         *     last price, the settings and their version, the guards' counts,
         *     the bots with their balances and errors, the running and queued
         *     events, the price band (anchor, where the makers quote, whether
         *     they walk toward a target beyond the band), the watchdog and the
         *     perpetual. Needs reports.read.
         */
        get: operations["getSim"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The target and the last price every 10 seconds */
        get: operations["getSimHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/events": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The price events running and queued, or the latest of every status */
        get: operations["listSimEvents"];
        put?: never;
        /**
         * Create a price event
         * @description Needs sim.control and a reason. market-sim runs it (at once or at
         *     starts_at, at most 24 hours ahead) in the administrator's name;
         *     beyond one operator's share of price moves in an hour (30% at once,
         *     50% an hour) it becomes a request for a second administrator with
         *     sim.control (202, decided on the approvals), whose name market-sim
         *     then receives as the approver. 403 SIM_EVENTS_OFF while the flag
         *     sim.events is off; 400 beyond the hard caps.
         */
        post: operations["createSimEvent"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/events/{id}/end": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel a queued event or end a running one
         * @description A HALT ends by resuming the pair and its perpetual. Needs sim.control.
         */
        post: operations["endSimEvent"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/events/{id}/plan": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A threshold target's plan and where the market is against it
         * @description ASTRA A6: a TARGET's envelope minute by minute (the planned price
         *     with its band), its spikes, and now: the target price, the planned
         *     one, the deviation ln(target/plan), whether the target is at risk
         *     of missing the level, when it crossed and how it ended (HIT,
         *     MISSED, CANCELED). market-sim's answer as it is. Needs
         *     reports.read.
         */
        get: operations["getSimPlan"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/target-preview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Whether a threshold target is feasible, and its envelope, before it is asked for
         * @description ASTRA A6, for the price control's form: from the current target,
         *     whether the level is reachable within the window (the shortest
         *     window that is, min_duration_seconds), the move it plans, whether
         *     it is beyond one operator's share (a second administrator approves
         *     it), and the planned envelope. Needs reports.read.
         */
        get: operations["previewSimTarget"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/params": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Replace the simulated market's settings
         * @description Every field, as GET /admin/v1/sim shows them. Needs sim.control.
         *     Changes that move the price or the turnover beyond one operator's
         *     share become a request for a second administrator (202).
         */
        put: operations["updateSimParams"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/impact": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * What a mark price would do to the simulated market's perpetual
         * @description ASTRA design §6.3: the open longs and shorts, the positions the
         *     margin monitor would take over at that price that it does not now
         *     (a cross account whole), their notional and accounts, and what they
         *     lack beyond their margin (the insurance fund's estimated share);
         *     HOUSE left out (derivatives-service's price impact). Changes
         *     nothing. Needs reports.read.
         */
        post: operations["simImpact"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/token": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Who holds the simulated market's coin
         * @description The coin (the base of market-sim's pair, ASTRA) as the ledger's
         *     lines hold it (the read model, a few seconds behind): the bots
         *     (market-sim's list) and the other users with how many of each hold
         *     some, the platform's system accounts, what manual adjustments
         *     issued (the ADJUSTMENT account's debit) and the 20 largest holders.
         *     Needs reports.read.
         */
        get: operations["getSimToken"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/sim/mint": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * More of the coin or USDT for the bots
         * @description A fund operation of kind SIM_MINT (ASTRA design §4; no transfers
         *     between bots, the pool grows by audited adjustments): the amount
         *     split evenly, to two decimal places, over the bots (of one role, or
         *     every one), the first taking the rounding; one manual adjustment of
         *     each bot's SPOT account, keyed by the operation and the bot.
         *     Booked at once in single-person mode within the limits (EXECUTED,
         *     or FAILED with the ledger's refusal of the first bot), otherwise
         *     PENDING for a second administrator with ledger.adjust.approve;
         *     COMMON_UNAVAILABLE with the detail approval_id when the ledger does
         *     not answer. A refusal after some bots were booked answers the
         *     ledger's code with the details approval_id, bot, booked and of: the
         *     operation stays PENDING (counted in its requester's 24 hours) to be
         *     finished on the approvals page once the cause is fixed, its keys
         *     booking only the rest. At most 10,000,000 of the coin or 1,000,000
         *     USDT at once, whoever approves (422 ADMIN_SIM_MINT_CAP, details
         *     asset and max). Needs ledger.adjust.request.
         */
        post: operations["mintSimBots"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/platform/profile": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The platform's profile, as the sites show it
         * @description Design 2026-10-04 §4.1 (D2): the name, logos, colours, footer,
         *     contact, learning banner, registration and the welcome credits the
         *     sites read, with who last changed it. instrument-service keeps it;
         *     the welcome credits are the ledger's (changed with
         *     /admin/v1/platform/welcome-credits). Every administrator reads it.
         */
        get: operations["getPlatformProfile"];
        /**
         * Replace the platform's profile (all but the images and the welcome credits)
         * @description expected_version is the version read: 409
         *     INSTRUMENT_PLATFORM_CHANGED when someone saved in between. The
         *     sites show the change within a minute, without a build. One ADMIN
         *     (settings.write) alone; audited as admin.platform.updated with the
         *     fields that changed, before and after (design §5).
         */
        put: operations["updatePlatformProfile"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/platform/images/{kind}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kind: "logo_light" | "logo_dark" | "favicon" | "apple_touch_icon";
            };
            cookie?: never;
        };
        get?: never;
        /**
         * Upload one of the platform's images
         * @description Square, at most 200 KB; PNG, SVG (rebuilt from an allow list) or
         *     WebP for the logos, PNG or SVG for the favicon, PNG of at least
         *     180 px for the apple-touch-icon (instrument-service checks them).
         *     Its URL changes with the version, so no cache serves the old one.
         *     settings.write; audited as admin.platform.image_updated with its
         *     type, size and SHA-256, never its bytes.
         */
        put: operations["uploadPlatformImage"];
        post?: never;
        /**
         * Remove an uploaded image (the built-in one shows again)
         * @description settings.write; audited as admin.platform.image_removed.
         */
        delete: operations["deletePlatformImage"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/platform/welcome-credits": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * What a new account gets (the ledger's setting)
         * @description The welcome credits (design 2026-10-04 §4.2) and the master switch
         *     ledger.welcome_credit: a new account gets them while both say so.
         *     A launch sets them to nothing. Every administrator reads them.
         */
        get: operations["getWelcomeCredits"];
        /**
         * Change the welcome credits
         * @description settings.write. Lowering or clearing (an amount of 0 or [] is
         *     nothing) applies at once: 200 with the setting, audited as
         *     admin.platform.welcome_changed (old, new). Raising any asset, or
         *     from nothing to something, waits for a second ADMIN: 202 with a
         *     WELCOME_CREDIT request (escalation WELCOME_RAISE). A raise is
         *     worth at most 10,000 USDT a change (422 ADMIN_WELCOME_RAISE_CAP,
         *     whoever approves), priced at the USDT pairs' fresh prices (422
         *     ADMIN_WELCOME_UNPRICED without one). expected_version is the
         *     version read: 409 LEDGER_SETTINGS_CHANGED when it moved, then and
         *     when the request is approved (it fails).
         */
        put: operations["setWelcomeCredits"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/launch-checklist": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * What of the learning setup is still on (read-only)
         * @description Design 2026-10-04 §4.6 (D2): each item from its source now, with
         *     what it is (value) and whether it is what a launch needs. ready
         *     when every item is OK. It changes nothing and covers what the
         *     console can change and see; the deployment side is the launch
         *     handbook's. PENDING: its source is not there yet; UNKNOWN: its
         *     source did not answer. Every administrator reads it.
         */
        get: operations["getLaunchChecklist"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/assets/{code}/profile": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                code: string;
            };
            cookie?: never;
        };
        /**
         * An asset's profile, as the sites show it
         * @description The name, introductions, links and logo the sites show for an
         *     asset beyond its code (ASTRA design §5.3; instrument-service keeps
         *     it). The logo is at logo_url, a path the console's domain also
         *     serves. Needs instruments.read.
         */
        get: operations["getAssetProfile"];
        /**
         * Replace an asset's profile
         * @description The text replaces the old (an empty display name shows the asset's
         *     name); logo (base64, with logo_mime: PNG, SVG or WebP, square, at
         *     most 200 KB; an SVG is rebuilt from an allow list of elements)
         *     replaces the logo, clear_logo removes it, neither keeps it. The
         *     sites show it within a minute: a new logo gets a new URL. Audited
         *     as admin.instruments.profile_updated on asset:<code>, the logo by
         *     its type and size. Needs instruments.write.
         */
        put: operations["updateAssetProfile"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments/changes": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The changes of trading parameters, newest first
         * @description Waiting for a second ADMIN (PENDING_APPROVAL) or their time
         *     (SCHEDULED), applied, canceled, rejected or failed (the target
         *     moved since it was confirmed). Needs instruments.read.
         */
        get: operations["listInstrumentChanges"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments/changes/{id}/decide": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve or reject a change waiting for a second ADMIN
         * @description Approved, it takes effect change_delay_seconds later; not by its
         *     requester (ADMIN_SELF_APPROVAL, who cancels instead);
         *     ADMIN_CHANGE_CLOSED once decided. Audited as
         *     admin.instruments.change_approved or change_rejected. Needs
         *     instruments.trading.
         */
        post: operations["decideInstrumentChange"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments/changes/{id}/cancel": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Cancel a change before it takes effect
         * @description Any ADMIN, its requester included; ADMIN_CHANGE_CLOSED once it took
         *     effect or was closed, ADMIN_CHANGE_APPLYING while an apply round has
         *     it. Audited as admin.instruments.change_canceled. Needs
         *     instruments.trading.
         */
        post: operations["cancelInstrumentChange"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments/pairs/{symbol}/status/preview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * What moving a trading pair to another status does
         * @description A halt takes effect at once (`immediate`, the emergency brake);
         *     anything else is a change of trading parameters: the answer carries
         *     the confirmation POST .../status brings back, and how long the
         *     change will wait. INSTRUMENT_STATUS_TRANSITION_INVALID for a move
         *     the state machine forbids. Needs instruments.trading.
         */
        post: operations["previewPairStatus"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/instruments/pairs/{symbol}/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Move a trading pair to another status (the per-pair emergency switch)
         * @description PREPARE → TRADING ⇄ HALT; TRADING or HALT → CANCEL_ONLY →
         *     DELISTED (CANCEL_ONLY is the way out: it cannot return to
         *     trading). Fails with INSTRUMENT_STATUS_TRANSITION_INVALID
         *     otherwise. A halt takes effect at once (200); any other move needs
         *     the preview's confirmation (POST .../status/preview) and answers
         *     202 with the change that waits (see POST
         *     /admin/v1/instruments/apply). Needs instruments.trading (ADMIN).
         */
        post: operations["setPairStatus"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/flags": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Feature flags (requirements §5.14)
         * @description Every flag the platform knows; one never set is off, with version 0. Needs flags.read.
         */
        get: operations["listFlags"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/flags/{key}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Turn a flag on or off
         * @description Keeps the flag's rules (regions, statuses, allow lists); services
         *     pick the change up within 5 seconds. Fails with COMMON_NOT_FOUND
         *     for a key the platform does not define. Needs flags.write.
         */
        put: operations["switchFlag"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/ledger/adjustments": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Request a manual adjustment of a user's balance
         * @description Creates a PENDING request for another administrator with
         *     ledger.adjust.approve (escalation REQUESTED), unless `direct` asks
         *     to carry it out at once, which works as POST
         *     /admin/v1/users/{id}/adjustments. A positive amount credits the
         *     user's SPOT (or FUTURES) account against the ADJUSTMENT system
         *     account, a negative one debits it. Needs ledger.adjust.request.
         */
        post: operations["requestAdjustment"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/approvals": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Fund operations, newest first
         * @description Two-person requests and single-person operations. Needs ledger.adjust.request or audit.read.
         */
        get: operations["listApprovals"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/approvals/{id}/sim-preview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A simulated market's request measured now, for its decider
         * @description When the request lapses (a day after it was asked for, or when its
         *     event was to start), the target price now, where the change would
         *     take the price (a jump's or a target event's price, the settings'
         *     new anchor; null for changes that move no price directly), that
         *     move now beside the one market-sim measured when it was asked for,
         *     and what the price would do to the perpetual (as /sim/impact; null
         *     when not measured). Needs reports.read.
         */
        get: operations["simApprovalPreview"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/approvals/{id}/decide": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Approve (and book) or reject a pending fund operation
         * @description Approving one's own request fails with ADMIN_SELF_APPROVAL, except
         *     a single-person operation whose outcome was unknown (its requester
         *     finishes it); rejecting one's own request withdraws it. A decided
         *     one fails with ADMIN_APPROVAL_DECIDED, unless the decider took the
         *     same decision: then it answers with the operation as that decision
         *     left it (a retry). Approving books the operation with the
         *     idempotency key approval:<id>: EXECUTED with the journal, or FAILED
         *     when the ledger refuses (for instance LEDGER_ADJUSTMENT_DISABLED
         *     while the flag ledger.manual_adjustment is off). A fund operation
         *     is marked attempted (attempted_at) before it is booked: when the
         *     ledger cannot be reached it stays PENDING with how the attempt
         *     ended in result, the error's details name it (approval_id), and it
         *     can be approved again but no longer rejected
         *     (ADMIN_APPROVAL_ATTEMPTED). Needs ledger.adjust.approve;
         *     a simulated market's change (SIM_EVENT, SIM_PARAMS) needs
         *     sim.control instead, and approving one that lapsed (a day after it
         *     was asked for, or when its event was to start) fails it, result
         *     "expired at <time>", nothing sent to market-sim.
         */
        post: operations["decideApproval"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/audit-logs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The audit trail, newest first
         * @description From ClickHouse audit_logs, which lags the change by a few seconds.
         *     Needs audit.read.
         */
        get: operations["searchAuditLogs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/audit-logs/export": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The audit entries matching the filters, as CSV
         * @description The search's filters, newest first, at most 10,000 rows; the header
         *     X-Truncated is true when more were left out (narrow the time
         *     range). UTF-8 with a byte order mark, so spreadsheets read Chinese;
         *     columns occurred_at, event_type, actor, target, action, reason,
         *     details, event_id (action, reason and details are the
         *     administrator action's, or a configuration change's old and new
         *     values). Written a page at a time (X-Truncated is known before
         *     the rows, from a count). The export is itself audited as
         *     admin.audit.exported. It has email and IP addresses: needs
         *     audit.export (ADMIN, AUDITOR; C5.5 ⑪).
         */
        get: operations["exportAuditLogs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/reports/trading": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Trades and orders per symbol and day, week or month (UTC), newest first
         * @description From the ClickHouse read models (trades, order_updates), which lag
         *     the services by a few seconds. Needs reports.read.
         */
        get: operations["tradingReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/reports/wallet": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Credited deposits and confirmed withdrawals per asset and day, week or month (UTC)
         * @description Deposits booked to users (not the unclaimed ones) by the day they
         *     were credited; withdrawals by the day they were confirmed. Needs
         *     reports.read.
         */
        get: operations["walletReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/reports/candles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Candles of a symbol from the trades read model, newest first
         * @description Intervals are aligned to the epoch in UTC. Needs reports.read.
         */
        get: operations["candleReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/reports/derivatives": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Each perpetual contract's trading, funding and liquidations per day, week or month (UTC)
         * @description From the ClickHouse contract read models (derivatives_fills,
         *     derivatives_funding, derivatives_liquidations): settled fills and
         *     their fees and results, the funding the positions paid and
         *     received at the day's settlements, the positions taken over, the
         *     auto-deleveraged counterparties and what the insurance fund paid.
         *     Needs reports.read.
         */
        get: operations["derivativesReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/reports/open-interest": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Each contract's open positions from the positions read model
         * @description The latest position snapshots (derivatives_positions); a few
         *     seconds behind derivatives-service. Needs reports.read.
         */
        get: operations["openInterestReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/reports/users": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The users' activity per day, week or month (UTC), oldest first
         * @description Per bucket: the accounts registered and signed in (the auth
         *     events), those trading (either side of a spot trade, or a
         *     contract fill) and those with a deposit credited, each counted
         *     once; total is every account registered by the bucket's end.
         *     HOUSE and the simulated market's bots are left out (partial names
         *     "bots" when market-sim could not say which they are). Every bucket
         *     of the period comes, empty ones too. Needs reports.read.
         */
        get: operations["usersReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/reports/house-pnl": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * HOUSE's result per day, week or month (UTC) in USDT, oldest first
         * @description Spot (ADR-0013, ADR-0015): HOUSE's trading valued day by day, the
         *     quote it got less paid plus the base it holds from trading at each
         *     pair's last price that day, in USDT at the last price of the quote
         *     asset's USDT pair; spot_pnl is its change over the bucket and
         *     spot_result its level since HOUSE began, at the bucket's end. A
         *     pair without a price when one is needed is left out of every day
         *     (unpriced). Contracts: the realized results of HOUSE's fills less
         *     their fees, and the funding it got (negative when it paid). total
         *     sums the three; cumulative runs over the period. Needs
         *     reports.read.
         */
        get: operations["housePnLReport"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/derivatives/contracts": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Each perpetual contract's status, reduce-only state, mark price and open interest
         * @description Live from derivatives-service. A contract goes reduce-only by
         *     itself when its index or mark price fails (requirements §11.7:
         *     risk.events SystemDegraded); only a person lifts it. Needs
         *     derivatives.read.
         */
        get: operations["listContractStates"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/derivatives/contracts/{symbol}/status": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Move a perpetual contract to another status
         * @description The same transitions as trading pairs (PREPARE → TRADING ⇄ HALT;
         *     TRADING or HALT → CANCEL_ONLY → DELISTED) and the same guard: a
         *     halt at once (200), any other move confirmed from its preview and
         *     waiting (202); instrument-service records it. Needs
         *     instruments.trading (ADMIN).
         */
        post: operations["setContractStatus"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/derivatives/contracts/{symbol}/status/preview": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * What moving a perpetual contract to another status does
         * @description As POST /admin/v1/instruments/pairs/{symbol}/status/preview. Needs instruments.trading.
         */
        post: operations["previewContractStatus"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/derivatives/contracts/{symbol}/lift-reduce-only": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * End a contract's reduce-only once its prices are sound again
         * @description lifted is false when the contract was not reduce-only. Check the
         *     mark price first: a contract whose mark is still stale degrades
         *     again within seconds. Needs derivatives.write.
         */
        post: operations["liftReduceOnly"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/derivatives/risk": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Positions warned, taken over by the liquidation engine or close to it
         * @description Live from derivatives-service: positions taken over, warned
         *     (margin balance at most 1.2 × maintenance margin) or with a margin
         *     ratio of at least 0.5, riskiest first. Cross positions are
         *     measured on their own here. Needs derivatives.read.
         */
        get: operations["listRiskPositions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/derivatives/liquidations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Liquidation steps, newest first
         * @description From the ClickHouse read model derivatives_liquidations. Needs derivatives.read.
         */
        get: operations["listLiquidations"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/positions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Every user's open contract positions, riskiest first
         * @description From derivatives-service, valued at the mark price: highest margin
         *     ratio (maintenance margin / margin balance) first, then the
         *     largest; HOUSE's positions come last. Cross positions are measured
         *     on their own; their liquidation price is on the user's page, and
         *     they count as warned when their cross account is. mark_fresh says
         *     whether the mark price is fresh. At most `limit` (500) positions;
         *     `truncated` says there are more. Needs derivatives.read.
         */
        get: operations["listPositions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/derivatives/insurance-fund": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The insurance fund's balance
         * @description The ledger's INSURANCE_FUND system account, with PNL_CLEARING (the
         *     open positions' unsettled results, which may be negative). Needs
         *     derivatives.read.
         */
        get: operations["getInsuranceFund"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/derivatives/insurance-fund/contributions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Contribute simulated funds to the insurance fund
         * @description Creates a PENDING INSURANCE_FUND request for another administrator
         *     with ledger.adjust.approve, or with `direct` carries it out at once
         *     when single-person mode and its limits allow (as POST
         *     /admin/v1/users/{id}/adjustments does for an adjustment); booking
         *     it (ledger FundInsurance, INSURANCE_CONTRIBUTION from ADJUSTMENT)
         *     needs the flag ledger.manual_adjustment. Needs
         *     ledger.adjust.request.
         */
        post: operations["requestInsuranceFunding"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/house": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * HOUSE's book (virtual liquidity, ADR-0013 and ADR-0015)
         * @description HOUSE's spot inventory (the ledger's MARKET_MAKER accounts) valued
         *     at the last prices, what it traded per pair (the trades read model)
         *     and its result at those prices, and its perpetual contract
         *     positions (the account HOUSE_USER_ID). Internal assets go below
         *     zero when HOUSE sold them short. A part that cannot be read is left
         *     empty and named in `partial`. Needs reports.read.
         */
        get: operations["getHouse"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/health": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Every service's readiness
         * @description Asks each service's ops endpoint (/readyz) within 2 seconds; with
         *     details=true also reads its metrics (its version, its Kafka
         *     consumers' lag and the records they parked in a DLQ since it
         *     started) and the reference feed's state: the system health page.
         *     Needs reports.read.
         */
        get: operations["getHealth"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/ledger/system-balances": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The platform's system accounts
         * @description FEE_REVENUE, INSURANCE_FUND, MARKET_MAKER (HOUSE), PNL_CLEARING,
         *     ADJUSTMENT, DEPOSIT_PENDING and the rest, in one asset or all.
         *     Needs reports.read.
         */
        get: operations["getSystemBalances"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/ledger/reconciliation": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The ledger's invariant checks
         * @description The latest run of every check of the ledger's reconciliation (hourly,
         *     requirements §11.4) and the last 50 runs that found mismatches, with
         *     the first ten mismatches of each. Needs reports.read.
         */
        get: operations["getReconciliation"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/custody": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The custody wallet (ADR-0011)
         * @description The custodian's coins with their balances as it reports them now,
         *     matched to the networks that use them; the latest chain check of
         *     each holder and asset (the custodian and the platform's own
         *     wallets: held, expected by the ledger, held elsewhere, on its way
         *     out, unbooked fees, shortfall); the withdrawals with the custodian;
         *     and how many callbacks need a person. Without a configured
         *     custodian `configured` is false; an unreachable one leaves `coins`
         *     empty and says why in `error`. One custodian at a time (`provider`;
         *     its own checks and the platform wallets'). Needs withdrawals.read.
         */
        get: operations["getCustody"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/custody/callbacks": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The custodians' callbacks, newest first
         * @description Every callback as received, with its signature check and outcome; one custodian's when `provider` is given. Needs withdrawals.read.
         */
        get: operations["listCustodyCallbacks"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/custody/callbacks/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * One callback with the request as received
         * @description Needs withdrawals.read.
         */
        get: operations["getCustodyCallback"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/custody/callbacks/{id}/replay": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Apply a stored callback again
         * @description For a verified callback that FAILED, stayed UNMATCHED or RECEIVED:
         *     its signature is checked again (not its age) and it is applied as
         *     if it had just arrived, which is harmless for one already applied.
         *     Audited. Needs withdrawals.review.
         */
        post: operations["replayCustodyCallback"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/custody/fees": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The custodians' withdrawal fees, newest first
         * @description A custodian's fee on a withdrawal is booked from GAS_SUPPLY as it
         *     comes (BOOKABLE; journal_id null while GAS_SUPPLY is short), or
         *     held for a person (HELD: its unit on the network is not confirmed,
         *     or it looks wrong) who books or writes it off (C6). One
         *     custodian's when `provider` is given. Needs withdrawals.read.
         */
        get: operations["listCustodyFees"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/custody/fees/{withdrawal_id}/book": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Book a held fee from GAS_SUPPLY
         * @description As the custodian reported it, or in the asset and amount found
         *     charged (the platform must hold that asset with the custodian on
         *     the withdrawal's network, at most its decimals; else 400: write it
         *     off). wallet-service records the decision and audits it as
         *     wallet.custody.fee.book with the administrator as the actor, as
         *     exchangectl wallet custody-fee does; its processor books it within
         *     a round. As charged is at most 5 times what was reported: by
         *     amount in the reported asset, by worth in USDT at the last prices
         *     in another (422 ADMIN_FEE_ABOVE_REPORTED; ADMIN_FEE_UNPRICED when
         *     one of the two has no fresh price): more is booked with exchangectl
         *     wallet custody-fee (review ㉖). As charged, a fee not among the
         *     held ones is refused (409 ADMIN_FEE_NOT_HELD, review ㉗). A fee that waits for no one
         *     (booked, written off) is 409 WALLET_CUSTODY_FEE_NOT_HELD (details
         *     status), one another decision took first 409
         *     WALLET_CUSTODY_FEE_CHANGED; a withdrawal without a custodian's fee
         *     404 WALLET_CUSTODY_FEE_NOT_FOUND. Needs ledger.adjust.approve.
         */
        post: operations["bookCustodyFee"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/admin/v1/custody/fees/{withdrawal_id}/write-off": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Write a held fee off
         * @description For a fee not taken from the coin balances the platform holds, or
         *     reported in another unit: nothing is booked. Also a BOOKABLE one
         *     still waiting for GAS_SUPPLY. Audited by wallet-service as
         *     wallet.custody.fee.write_off. 409 WALLET_CUSTODY_FEE_NOT_HELD or
         *     _CHANGED when it waits for no one, 404 WALLET_CUSTODY_FEE_NOT_FOUND
         *     without a fee. Needs ledger.adjust.approve.
         */
        post: operations["writeOffCustodyFee"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        House: {
            assets: {
                asset: string;
                /** @description The asset has deposits or withdrawals; HOUSE must hold it to sell it. */
                backed: boolean;
                balance: components["schemas"]["Decimal"];
                /** @description USDT per unit (the last price of its USDT pair); null without one. */
                price: components["schemas"]["Decimal"] | null;
                value_usdt: components["schemas"]["Decimal"] | null;
            }[];
            pairs: {
                symbol: string;
                trades: number;
                bought_base: components["schemas"]["Decimal"];
                sold_base: components["schemas"]["Decimal"];
                paid_quote: components["schemas"]["Decimal"];
                got_quote: components["schemas"]["Decimal"];
                /** Format: date-time */
                last_at: string;
                /** @description Bought less sold. */
                net_base: components["schemas"]["Decimal"];
                /** @description Got less paid. */
                net_quote: components["schemas"]["Decimal"];
                price: components["schemas"]["Decimal"] | null;
                /** @description net_base at the last price plus net_quote (USDT pairs); null without a price. */
                pnl_usdt: components["schemas"]["Decimal"] | null;
            }[];
            /** @description HOUSE's open positions as derivatives-service renders a user's positions. */
            contracts: {
                [key: string]: unknown;
            }[];
            totals: {
                inventory_usdt: components["schemas"]["Decimal"];
                backed_usdt: components["schemas"]["Decimal"];
                /** @description Below zero while HOUSE is short on internal assets. */
                internal_usdt: components["schemas"]["Decimal"];
                pnl_usdt: components["schemas"]["Decimal"];
            };
            partial: ("prices" | "assets" | "inventory" | "trades" | "contracts")[];
        };
        ServiceHealth: {
            service: string;
            ready: boolean;
            latency_ms: number;
            error?: string;
            /** @description With details; the commit the service was built from. */
            version?: string;
            /** @description With details, for a service with Kafka consumers; records behind, summed over its consumers. */
            kafka_lag?: number;
            /** @description With details, for a service with Kafka consumers; records parked in a DLQ since it started. */
            dlq?: number;
            /** @description With details, whether the third parties the service uses are configured (exchange_config_present{item}), never their values. */
            config_present?: {
                [key: string]: boolean;
            };
        };
        ReconciliationRun: {
            /** @example JOURNAL_BALANCED */
            check: string;
            /** Format: date-time */
            started_at: string;
            mismatches: number;
            /** @description The first ten mismatches. */
            details: {
                key: string;
                detail: string;
            }[];
        };
        /** @description Pass as cursor for the next page; null on the last. */
        NextCursor: string | null;
        UserSummary: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            status: "ACTIVE" | "RISK_REVIEW" | "FROZEN" | "CLOSED";
            region: string;
            language: string;
            /** @description An IANA zone; empty for the browser's. */
            timezone?: string;
            kyc_level: number;
            /** Format: date-time */
            created_at: string;
            /** @description The console's tags on the account. */
            tags: string[];
        };
        Note: {
            /** Format: uuid */
            id: string;
            body: string;
            /** Format: uuid */
            admin_id: string;
            admin_email: string;
            /** Format: date-time */
            created_at: string;
        };
        Order: {
            /** Format: uuid */
            order_id: string;
            client_order_id: string;
            /** Format: uuid */
            user_id: string;
            symbol: string;
            /**
             * @description Empty for an order the trading service refused before it was accepted (its rejection carries no side or type).
             * @enum {string}
             */
            side: "BUY" | "SELL" | "";
            /**
             * @example LIMIT
             * @example MARKET
             */
            type: string;
            time_in_force: string;
            price: components["schemas"]["NullableDecimal"];
            quantity: components["schemas"]["NullableDecimal"];
            quote_amount: components["schemas"]["NullableDecimal"];
            status: string;
            filled_quantity: components["schemas"]["Decimal"];
            filled_quote: components["schemas"]["Decimal"];
            /** @description Why it was canceled or rejected; empty otherwise. */
            reason: string;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
            /** @description A simulated market's bot placed it (false while market-sim does not answer). */
            bot: boolean;
        };
        Trade: {
            /** Format: uuid */
            trade_id: string;
            symbol: string;
            /** Format: int64 */
            trade_number: number;
            price: components["schemas"]["Decimal"];
            quantity: components["schemas"]["Decimal"];
            quote_quantity: components["schemas"]["Decimal"];
            /** @enum {string} */
            taker_side: "BUY" | "SELL";
            buyer_user_id: string;
            buyer_order_id: string;
            seller_user_id: string;
            seller_order_id: string;
            buyer_is_maker: boolean;
            buyer_fee: components["schemas"]["Decimal"];
            seller_fee: components["schemas"]["Decimal"];
            /** Format: date-time */
            executed_at: string;
            /**
             * @description The side HOUSE took (its virtual liquidity, ADR-0015); empty between users.
             * @enum {string}
             */
            house_side?: "" | "BUY" | "SELL";
            /** @description The buyer is a simulated market's bot (false while market-sim does not answer). */
            buyer_bot: boolean;
            /** @description The seller is a simulated market's bot. */
            seller_bot: boolean;
        };
        Deposit: {
            /** Format: uuid */
            deposit_id: string;
            /** @description Empty for a deposit to no known address. */
            user_id: string;
            asset: string;
            network: string;
            /**
             * @example CHAIN
             * @example INTERNAL
             */
            kind: string;
            address: string;
            tx_hash: string;
            amount: components["schemas"]["Decimal"];
            status: string;
            unclaimed: boolean;
            reason: string;
            confirmations: number;
            required_confirmations: number;
            /** Format: date-time */
            updated_at: string;
        };
        /** @description The reference feed (market-data-service). */
        FeedStatus: {
            /** @enum {string} */
            state: "OFF" | "OK" | "DELAYED" | "DOWN";
            /** Format: date-time */
            received_at: string | null;
            followed: string[];
            /** @description Pairs halted because the feed was lost (market.halt_on_feed_loss). */
            halted: {
                symbol: string;
                /** Format: date-time */
                halted_at: string;
            }[];
        };
        Dashboard: {
            users: {
                /** Format: int64 */
                total: number;
                /** Format: int64 */
                new_24h: number;
            };
            trading: {
                /** Format: int64 */
                trades_24h: number;
                /** Format: int64 */
                active_traders_24h: number;
                turnover_24h: {
                    quote_asset: string;
                    amount: components["schemas"]["Decimal"];
                }[];
            };
            wallet: {
                /**
                 * Format: int64
                 * @description Deposits detected or confirming.
                 */
                pending_deposits: number;
                /**
                 * Format: int64
                 * @description Withdrawals waiting for review.
                 */
                pending_withdrawals: number;
            };
            risk: {
                /** Format: int64 */
                events_24h: number;
            };
            /** @description The reference feed; null when market-data-service could not be asked. */
            feed: null | components["schemas"]["FeedStatus"];
            /** @description One per UTC day, oldest first. */
            series: {
                /** Format: date */
                day: string;
                /** Format: int64 */
                new_users: number;
                /** Format: int64 */
                trades: number;
                turnover_usdt: components["schemas"]["Decimal"];
            }[];
            /** @description The parts that could not be read (users, activity, feed). */
            partial: string[];
        };
        /** @example 12.5 */
        Decimal: string;
        /** @description The model's and the bots' settings by name (numbers; docs/runbook/market-sim.md「设置」). */
        SimParams: {
            [key: string]: number;
        };
        SimStatus: {
            symbol: string;
            enabled: boolean;
            running: boolean;
            target_price: string | null;
            last_price: string | null;
            references_fresh: boolean;
            params: components["schemas"]["SimParams"];
            version: number;
            guards: {
                [key: string]: number;
            };
            bots: components["schemas"]["SimBot"][];
            events: components["schemas"]["SimEvent"][];
            /** @description The perpetual on the coin; empty without one. */
            perp: string;
            perp_running: boolean;
            anchor_price: string | null;
            price_band: string;
            quote_center: string | null;
            walking: boolean;
            /** @description The target's distance from the anchor in bands (within ±1 inside the band). */
            band_distance: number | null;
            /** Format: date-time */
            last_trade_at: string | null;
            watchdog: {
                fired: number;
                /** Format: date-time */
                last_at: string | null;
            };
            /** Format: date-time */
            at: string | null;
        };
        SimBot: {
            user_id: string;
            /** @enum {string} */
            role: "MAKER" | "TAKER" | "TREND" | "EXECUTOR";
            label: string;
            enabled: boolean;
            balances_known: boolean;
            usdt: string;
            coin: string;
            perp_position: string;
            futures_usdt: string;
            error: string;
            /** Format: date-time */
            error_at: string | null;
            /** Format: date-time */
            retry_at: string | null;
        };
        SimEvent: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            type: "JUMP" | "TARGET" | "TREND" | "VOLATILITY" | "PAUSE" | "HALT" | "REANCHOR" | "SPIKE";
            size: number;
            price: string | null;
            mu: number;
            factor: number;
            duration_seconds: number;
            hold_seconds: number;
            /** Format: date-time */
            starts_at: string;
            /** @enum {string} */
            status: "SCHEDULED" | "RUNNING" | "DONE" | "CANCELED";
            created_by: string;
            approved_by: string;
            reason: string;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            started_at: string | null;
            /** Format: date-time */
            ended_at: string | null;
            from_price: string | null;
            ended_by: string;
            /** @description A TARGET's side of its level (ABOVE, BELOW; ASTRA A6); empty or null otherwise. */
            direction?: string | null;
            /** @description What a TARGET does once crossed (FOLLOW, HOLD). */
            then?: string | null;
            /** @description How a TARGET ended (HIT, MISSED, CANCELED); empty while running. */
            result?: string | null;
            /** Format: date-time */
            crossed_at?: string | null;
            /** Format: date-time */
            ends_at?: string | null;
            /**
             * Format: date-time
             * @description When a TARGET's closing window starts (its last 10%, at least a minute); no spike starts in it.
             */
            closing_at?: string | null;
            /** Format: date-time */
            hold_until?: string | null;
            /** @description A SPIKE's TARGET. */
            parent_id?: string | null;
            /** @description A SPIKE's width. */
            width_seconds?: number | null;
            /** @description A TARGET's spikes, made with it (in the answer to its creation only). */
            spikes?: components["schemas"]["SimEvent"][];
        };
        SimEventWrite: {
            /** @enum {string} */
            type: "JUMP" | "TARGET" | "TREND" | "VOLATILITY" | "PAUSE" | "HALT" | "REANCHOR" | "SPIKE";
            /** @description JUMP's move (0.1 is +10%); a SPIKE's, a share of the planned price (at most 0.05 alone, 0.10 approved). */
            size?: number;
            /** @description TARGET's level. */
            price?: string;
            /** @description TREND's drift a day. */
            mu?: number;
            /** @description VOLATILITY's factor. */
            factor?: number;
            duration_seconds?: number;
            /** @description A TARGET held this long at its level once crossed (then HOLD; at most a day). */
            hold_seconds?: number;
            /**
             * Format: date-time
             * @description At most 24 hours ahead (else 400 ADMIN_SIM_TOO_FAR_AHEAD); a time already past is now, kept in the audit and the request as asked_starts_at.
             */
            starts_at?: string;
            /**
             * @description A TARGET's side of its level (ASTRA A6); inferred from the current target when absent.
             * @enum {string}
             */
            direction?: "ABOVE" | "BELOW";
            /**
             * @description What a TARGET does once crossed; FOLLOW when absent.
             * @enum {string}
             */
            then?: "FOLLOW" | "HOLD";
            /** @description Spikes a TARGET plans, each becoming its own SPIKE event (all or nothing). */
            spikes?: components["schemas"]["SimSpikeWrite"][];
            /** @description A SPIKE's width; 20 when absent. */
            width_seconds?: number;
        };
        SimSpikeWrite: {
            /** Format: date-time */
            at: string;
            /** @description A share of the planned price, e.g. -0.04. */
            size: number;
            width_seconds?: number;
        };
        SimPlanPoint: {
            /** Format: date-time */
            at: string;
            plan: string | number;
            low: string | number;
            high: string | number;
        };
        SimPlan: {
            event_id: string;
            direction?: string | null;
            level?: string | number | null;
            from_price?: string | number | null;
            /** Format: date-time */
            starts_at?: string | null;
            /** Format: date-time */
            closing_at?: string | null;
            /** Format: date-time */
            ends_at?: string | null;
            /** Format: date-time */
            hold_until?: string | null;
            /** @description The target's status (SCHEDULED, RUNNING, DONE, CANCELED). */
            status?: string | null;
            points: components["schemas"]["SimPlanPoint"][];
            spikes?: components["schemas"]["SimEvent"][];
            /** @description Where the target is against the plan, while it runs. */
            now?: {
                /** Format: date-time */
                at?: string;
                target?: string | number | null;
                plan?: string | number | null;
                low?: string | number | null;
                high?: string | number | null;
                /** @description ln(target/plan). */
                deviation?: number | null;
                at_risk?: boolean;
                /** Format: date-time */
                crossed_at?: string | null;
                result?: string | null;
            } | null;
        };
        SimTargetPreview: {
            /**
             * Format: date-time
             * @description The server's time, which the form takes its spikes' times from (review 29; a browser's clock may be off).
             */
            now?: string;
            /** @description The side of the level, as asked or inferred from the current target (ABOVE, BELOW). */
            direction?: string | null;
            feasible: boolean;
            /** @description The shortest window in which the level is reachable from the current target. */
            min_duration_seconds: number;
            /** @description The planned move, |ln(level/current target)|. */
            move: number;
            /** @description Beyond one operator's share (30% alone, 50% an hour): a second administrator approves it. */
            needs_approval: boolean;
            points: components["schemas"]["SimPlanPoint"][];
        };
        SimSample: {
            /** Format: date-time */
            at: string;
            target_price: string;
            last_price: string | null;
        };
        /** @description derivatives-service's price impact (the margin monitor's rules at the target), with the sides open now. */
        SimImpact: {
            symbol: string;
            target_price: components["schemas"]["Decimal"];
            /** @description The open positions, HOUSE's apart. */
            positions: number;
            /** @description The open longs; null when the positions cannot all be read. */
            longs: number | null;
            shorts: number | null;
            /** @description The positions the monitor would take over at the target that it does not now (a cross account whole). */
            liquidated: number;
            notional: components["schemas"]["Decimal"];
            accounts: number;
            /** @description What the liquidated lack beyond their margin at the target, for the insurance fund. */
            insurance_cost: components["schemas"]["Decimal"];
            /** @description The positions left out for want of a fresh mark price now. */
            unmeasured: number;
            /** @description The largest of the liquidated, 20 at most. */
            examples: {
                user_id: string;
                symbol: string;
                position_side: string;
                cross: boolean;
                notional: string;
                margin_balance: string;
                maintenance_before: string;
                maintenance_after: string;
            }[];
        };
        SimPreview: {
            /** Format: date-time */
            expires_at: string;
            expired: boolean;
            target_price: components["schemas"]["NullableDecimal"];
            expected_price: components["schemas"]["NullableDecimal"];
            /** @description expected_price ÷ target_price − 1, now. */
            move: number | null;
            /** @description The move market-sim measured when it was asked for. */
            requested_move: string | null;
            impact: components["schemas"]["SimImpact"] | null;
            /**
             * @description A target's spikes (A6, review 29): what its worst spike each way
             *     would do to the perpetual, measured where the mark goes at the
             *     tip (about half the spike, from the lower of the target now and
             *     the level down, from the higher up). Empty for anything else.
             */
            spike_impacts?: {
                price: components["schemas"]["Decimal"];
                impact: components["schemas"]["SimImpact"];
            }[];
        };
        SimHolding: {
            amount: components["schemas"]["Decimal"];
            /** @description How many accounts hold some. */
            holders: number;
        };
        SimToken: {
            /** @example ASTRA */
            asset: string;
            /** @description The pair's last trade as market-sim reads it; null before the first. */
            price: components["schemas"]["NullableDecimal"];
            bots: components["schemas"]["SimHolding"];
            /** @description Every other user (HOUSE included, when it holds the coin). */
            users: components["schemas"]["SimHolding"];
            /** @description The system accounts holding some (fees and the like), ADJUSTMENT apart. */
            platform: {
                /** @example FEE_REVENUE */
                account_type: string;
                amount: components["schemas"]["Decimal"];
            }[];
            /** @description What manual adjustments issued (the ADJUSTMENT account's debit); the bots, users and platform hold it all. */
            issued: components["schemas"]["Decimal"];
            /** @description The largest holders, largest first (20 at most). */
            top: {
                /** Format: uuid */
                user_id: string;
                amount: components["schemas"]["Decimal"];
            }[];
            /** Format: date-time */
            at: string;
        };
        AssetProfile: {
            /** @description Replaces the asset's name on the sites when set. */
            display_name: string;
            /** @description Introductions by language (zh-CN, en), up to 1,000 characters each. */
            description: {
                [key: string]: string;
            };
            /** @description https URLs by kind (website, explorer, whitepaper). */
            links: {
                [key: string]: string;
            };
            /** @description image/png, image/svg+xml or image/webp; empty without a logo. */
            logo_mime: string;
            logo_size: number;
            /** @description The logo's path with its version (/v1/market/assets/{code}/logo?v=...); empty without one. */
            logo_url: string;
            version: number;
        };
        AssetProfileWrite: {
            display_name: string;
            description: {
                [key: string]: string;
            };
            links: {
                [key: string]: string;
            };
            /** @description A new logo, base64. */
            logo?: string;
            /** @enum {string} */
            logo_mime?: "image/png" | "image/svg+xml" | "image/webp";
            clear_logo?: boolean;
        };
        /** @description A text by language; zh-CN stands in for a language without one. */
        PlatformTexts: {
            "zh-CN": string;
            en: string;
        };
        WelcomeCredit: {
            asset: string;
            amount: components["schemas"]["Decimal"];
        };
        WelcomeCreditsSetting: {
            credits: components["schemas"]["WelcomeCredit"][];
            /** @description ledger.welcome_credit, the master switch (both must hold for a grant). */
            flag_enabled: boolean;
            /** Format: int64 */
            version: number;
            /** @description "system:WELCOME_FUNDS" for the first value taken from the environment. */
            updated_by: string;
            /** Format: date-time */
            updated_at: string | null;
        };
        /** @description The platform's profile (as api/openapi/platform.yaml's PlatformProfile) and who last changed it. */
        PlatformProfileAdmin: {
            name: string;
            short_name: string;
            /** @description The PC site's host name (the mobile site m.<domain>, the console admin.<domain>); empty until set. */
            domain: string;
            theme_color: string;
            brand_color: string;
            images: {
                logo_light: string | null;
                logo_dark: string | null;
                favicon: string | null;
                apple_touch_icon: string | null;
            };
            footer: {
                copyright: components["schemas"]["PlatformTexts"];
                compliance: components["schemas"]["PlatformTexts"];
            };
            contact: {
                email: string;
                support_url: string | null;
            };
            social: {
                kind: string;
                url: string;
            }[];
            /** @enum {string} */
            default_locale: "zh-CN" | "en";
            learning_mode: {
                enabled: boolean;
                text: components["schemas"]["PlatformTexts"];
            };
            registration: {
                /** @enum {string} */
                status: "OPEN" | "CLOSED";
                closed_text: components["schemas"]["PlatformTexts"];
            };
            /** @description The ledger's, read within the last minute (change them at /admin/v1/platform/welcome-credits). */
            welcome_credits: {
                asset: string;
                amount: string;
            }[];
            /** Format: int64 */
            version: number;
            /** Format: date-time */
            updated_at: string;
            /** @description The actor of the last change ("admin:<email>", "system:migration"). */
            updated_by: string;
        };
        PlatformProfileWrite: {
            name: string;
            short_name: string;
            /** @description The PC site's host name (a.b, lower case, at most 253), or empty. */
            domain: string;
            theme_color: string;
            brand_color: string;
            footer: {
                copyright: components["schemas"]["PlatformTexts"];
                compliance: components["schemas"]["PlatformTexts"];
            };
            contact: {
                /** @description An e-mail address, or empty. */
                email: string;
                /** @description An https URL of at most 300 characters. */
                support_url: string | null;
            };
            social: {
                /** @enum {string} */
                kind: "x" | "telegram" | "discord" | "youtube" | "facebook" | "instagram" | "linkedin" | "reddit" | "medium" | "github" | "tiktok" | "weibo";
                /** @description An https URL of at most 300 characters. */
                url: string;
            }[];
            /** @enum {string} */
            default_locale: "zh-CN" | "en";
            learning_mode: {
                enabled: boolean;
                text: components["schemas"]["PlatformTexts"];
            };
            registration: {
                /** @enum {string} */
                status: "OPEN" | "CLOSED";
                closed_text: components["schemas"]["PlatformTexts"];
            };
            /** Format: int64 */
            expected_version: number;
        };
        LaunchItem: {
            /** @enum {string} */
            key: "welcome_credits" | "learning_mode" | "registration" | "admin_totp" | "two_person" | "test_assets" | "custodian" | "withdraw" | "brand" | "coin_profile" | "legal" | "third_party" | "admins" | "domain";
            /** @enum {string} */
            status: "OK" | "FAIL" | "PENDING" | "UNKNOWN";
            /** @description What it is now, by item (a flag's enabled and rules, the credits, the custodian's gateway host, the administrators...). */
            value: {
                [key: string]: unknown;
            };
        };
        LaunchChecklist: {
            /** @description Every item is OK. */
            ready: boolean;
            items: components["schemas"]["LaunchItem"][];
            /** Format: date-time */
            checked_at: string;
        };
        Reason: {
            reason: string;
        };
        Transition: {
            from: string;
            to: string;
        };
        /** @description By locale; zh-CN required, en optional. */
        LocalizedText: {
            "zh-CN": string;
            en?: string;
        };
        ArticleText: {
            /** @enum {string} */
            locale: "zh-CN" | "en";
            title: string;
            summary: string;
            /** @description Markdown. */
            body: string;
        };
        ArticleWrite: {
            slug: string;
            /** @description Announcements notice or product; help account, funds, trading, futures or faq (others allowed). */
            category: string;
            pinned: boolean;
            order: number;
            texts: components["schemas"]["ArticleText"][];
        };
        ContentArticle: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            section: "ANNOUNCEMENT" | "HELP" | "LEGAL" | "HOME";
            slug: string;
            category: string;
            pinned: boolean;
            order: number;
            /** @enum {string} */
            status: "DRAFT" | "PUBLISHED" | "ARCHIVED";
            /**
             * Format: date-time
             * @description A published article shows from then on (later than now when scheduled).
             */
            publish_at: string | null;
            version: number;
            updated_by: string;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
            texts: components["schemas"]["ArticleText"][];
        };
        Broadcast: {
            /** Format: uuid */
            id: string;
            /**
             * @description Everyone, or named users (one, or a tag's when sent).
             * @enum {string}
             */
            audience: "ALL" | "USERS";
            /** @description The named users; 0 for everyone. */
            users: number;
            title: components["schemas"]["LocalizedText"];
            body: components["schemas"]["LocalizedText"];
            link: string;
            email: boolean;
            /**
             * @description FAILED after ten failed rounds in a row; an operator resumes it (C5.5 ⑫).
             * @enum {string}
             */
            status: "SENDING" | "SENT" | "FAILED";
            /** @description Users who have it. */
            recipients: number;
            /** @description Of them, those who read it. */
            read: number;
            created_by: string;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            finished_at: string | null;
            /** @description Rounds failed in a row (each waits longer, 3 seconds doubling to 10 minutes). */
            failures: number;
            /** @description The last failed round's error; empty after a round that worked. */
            last_error: string;
            /**
             * Format: date-time
             * @description When the next round may run after a failure.
             */
            retry_at: string | null;
        };
        StatusRequest: {
            /** @enum {string} */
            to: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
            reason: string;
            /** @description The preview's confirmation.token; a halt needs none. */
            confirmation?: string;
        };
        StatusResult: {
            from: string;
            to: string;
            /** @description The change waiting; null for a halt, done at once. */
            change: null | components["schemas"]["InstrumentChange"];
        };
        /** @description Binds the confirming call to the preview an ADMIN saw (sealed, valid 10 minutes). */
        Confirmation: {
            token: string;
            /** Format: date-time */
            expires_at: string;
        };
        StatusPreview: {
            symbol: string;
            from: string;
            to: string;
            /** @description A halt, at once and without confirmation. */
            immediate: boolean;
            confirmation: null | components["schemas"]["Confirmation"];
            /** @description How long the change will wait once confirmed (or approved); 0 for a halt. */
            delay_seconds: number;
            /** @description A second ADMIN must approve it first (admin.two_person_approval). */
            two_person: boolean;
            /** @description The orders resting on the pair or contract (the read model, seconds behind; null when it cannot be read). A halt leaves them on the book and their owners may cancel them. */
            open_orders?: number | null;
        };
        /** @description A trading parameter a change moves (JSON values as stored; null for none). */
        ParamChange: {
            /** @enum {string} */
            entity: "FEE_SCHEDULE" | "TRADING_PAIR" | "CONTRACT";
            key: string;
            /** @enum {string} */
            field: "maker_fee_rate" | "taker_fee_rate" | "fee_tier" | "reference_symbol" | "reference_multiplier" | "risk_tiers" | "status";
            before: unknown;
            after: unknown;
        };
        /**
         * @description What a contract's new risk ladder would do to its open positions at
         *     the mark prices (HOUSE aside): positions the margin monitor would
         *     take over that it does not now (a cross account whole) with their
         *     notional and accounts, those newly warned, those above the risk
         *     limit of their leverage (they stay but cannot grow), those without
         *     a fresh mark price; the largest examples.
         */
        TierImpact: {
            symbol: string;
            positions: number;
            liquidated: number;
            notional: components["schemas"]["Decimal"];
            accounts: number;
            warned: number;
            over_limit: number;
            unmeasured: number;
            examples: {
                /** Format: uuid */
                user_id: string;
                symbol: string;
                position_side: string;
                cross: boolean;
                notional: components["schemas"]["Decimal"];
                margin_balance: components["schemas"]["Decimal"];
                maintenance_before: components["schemas"]["Decimal"];
                maintenance_after: components["schemas"]["Decimal"];
            }[];
        };
        /** @description What a preview says of a document's trading parameters. */
        ChangeGuard: {
            /** @description The trading parameters it moves; none means it applies at once. */
            params: components["schemas"]["ParamChange"][];
            impacts: components["schemas"]["TierImpact"][];
            /** @description Null without trading parameters, for a caller without instruments.trading, or while a ladder's impact cannot be measured (warning IMPACT_UNKNOWN). */
            confirmation: null | components["schemas"]["Confirmation"];
            delay_seconds: number;
            two_person: boolean;
        };
        /** @description A change of trading parameters an ADMIN confirmed (design 2026-10-02 §2 item 6). */
        InstrumentChange: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            kind: "CONFIG" | "PAIR_STATUS" | "CONTRACT_STATUS";
            /**
             * @example instruments
             * @example pair:BTC-USDT
             * @example contract:BTC-USDT-PERP
             */
            target: string;
            /** @enum {string} */
            status: "PENDING_APPROVAL" | "SCHEDULED" | "APPLIED" | "CANCELED" | "REJECTED" | "FAILED";
            reason: string;
            /** @description What the confirmation showed. */
            summary: {
                fingerprint?: string;
                /** @description A status change's status when confirmed. */
                from?: string;
                params: components["schemas"]["ParamChange"][];
                impacts: components["schemas"]["TierImpact"][];
                /** @description A document's items, created or updated. */
                items: {
                    entity: string;
                    key: string;
                    action: string;
                    version: number;
                }[];
            };
            /** Format: uuid */
            requested_by: string;
            requested_by_email: string;
            approved_by_email: string | null;
            /** Format: date-time */
            approved_at: string | null;
            /** @description Who canceled or rejected it. */
            closed_by_email: string | null;
            /** Format: date-time */
            closed_at: string | null;
            /**
             * Format: date-time
             * @description When it takes (or took) effect; null while it waits for approval or after it was closed unscheduled.
             */
            effective_at: string | null;
            /** Format: date-time */
            applied_at: string | null;
            /**
             * Format: date-time
             * @description An apply round has it (C5.5 ⑩): until its outcome is recorded it may have taken effect, so it is not canceled (ADMIN_CHANGE_APPLYING). A round whose call was answered by no one leaves it claimed, and a later one finds it in effect (result ends with "(in effect already)") or applies it.
             */
            applying_at: string | null;
            /** @description What applying it did, why it failed, or why it waits (a service down; ADMIN_IMPACT_GREW fails a ladder that would liquidate more positions when due than its confirmation showed). */
            result: string;
            /** Format: date-time */
            created_at: string;
        };
        Admin: {
            /** Format: uuid */
            id: string;
            email: string;
            name: string;
            role: components["schemas"]["AdminRole"];
            permissions: components["schemas"]["Permission"][];
            /** @description The password was generated for them (exchangectl admin create): only GET /admin/v1/me, POST /admin/v1/me/password and POST /admin/v1/logout are open until they change it (ADMIN_PASSWORD_CHANGE_REQUIRED, C5.5 ⑪). */
            must_change_password: boolean;
        };
        /** @enum {string} */
        AdminRole: "ADMIN" | "OPERATOR" | "FINANCE" | "AUDITOR";
        /** @enum {string} */
        Permission: "users.read" | "users.status" | "orders.cancel" | "instruments.read" | "instruments.write" | "flags.read" | "flags.write" | "withdrawals.read" | "withdrawals.review" | "ledger.adjust.request" | "ledger.adjust.approve" | "audit.read" | "reports.read" | "derivatives.read" | "derivatives.write" | "settings.write" | "users.notes" | "users.security" | "users.contacts" | "ledger.hold" | "deposits.review" | "admins.manage" | "instruments.trading" | "content.write" | "notices.send" | "sim.control" | "withdrawals.resume" | "audit.export";
        RolePermissions: {
            role: components["schemas"]["AdminRole"];
            permissions: components["schemas"]["Permission"][];
        };
        /** @description An administrator as the administrators page lists them. */
        ManagedAdmin: components["schemas"]["Admin"] & {
            /** @enum {string} */
            status: "ACTIVE" | "DISABLED";
            /** @description Failed sign-ins since the last success; five lock the account for 15 minutes. */
            failed_attempts: number;
            /** Format: date-time */
            locked_until: string | null;
            /** Format: date-time */
            last_login_at: string | null;
            /** Format: date-time */
            created_at: string;
            /** @description Live sessions. */
            sessions: number;
        };
        /** @description A one-time setup link, shown once to whoever created or reset the account to hand over (C5.5 ⑪). */
        AdminSetup: {
            /** @description On a create, the new administrator. */
            admin?: components["schemas"]["ManagedAdmin"];
            setup: {
                /** @description The console's setup page takes it as /setup#token=... */
                token: string;
                /** @enum {string} */
                kind: "CREATE" | "PASSWORD" | "TOTP";
                /** Format: date-time */
                expires_at: string;
            };
        };
        AdminSession: {
            ip: string;
            user_agent: string;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            last_seen_at: string;
            /** Format: date-time */
            expires_at: string;
        };
        Settings: {
            /** @description Fund operations need a second administrator (the flag admin.two_person_approval). */
            two_person_approval: boolean;
            /** @description How long a confirmed change of trading parameters waits before it takes effect (up to 86400, 300 by default), never below change_delay_floor_seconds. */
            change_delay_seconds: number;
            /** @description The least change_delay_seconds may be set to: admin-service's ADMIN_CHANGE_DELAY_FLOOR, 600 by default (one ADMIN alone cannot cut the wait to a minute, C5.5 ⑩). */
            change_delay_floor_seconds: number;
            /** @description In single-person mode, one fund operation is worth at most this much. */
            single_max_usdt: components["schemas"]["Decimal"];
            /** @description In single-person mode, an administrator's fund operations of the last 24 hours sum to at most this much. */
            daily_max_usdt: components["schemas"]["Decimal"];
            /** @description In single-person mode, one approval completes a withdrawal worth at most this much, however many reviewers it needs. */
            withdrawal_max_usdt: components["schemas"]["Decimal"];
            /** @description The caller's single-person fund operations of the last 24 hours (pending ones included). */
            daily_used_usdt: components["schemas"]["Decimal"];
            /** @description Who last changed the limits; empty for the defaults. */
            updated_by: string;
            /** Format: date-time */
            updated_at: string | null;
        };
        Todo: {
            /** @description Changes of trading parameters waiting for a second ADMIN or their time (with instruments.trading). */
            instrument_changes: number;
            /** @description Withdrawals in review (with withdrawals.read; counted up to 200). */
            withdrawals: number;
            /** @description Fund operations waiting for a decision (with ledger.adjust.request or ledger.adjust.approve). */
            approvals: number;
            /** @description Identity rebind requests waiting for a decision (with users.security; counted up to 200). */
            identity_requests: number;
            /** @description Deposits waiting for a decision (with deposits.review; counted up to 200). */
            deposits: number;
            partial: ("withdrawals" | "identity_requests" | "deposits")[];
        };
        Identity: {
            /** @enum {string} */
            kind: "EMAIL" | "PHONE";
            /** @description Masked except from POST /admin/v1/users/{id}/contacts/reveal. */
            value: string;
            /** Format: date-time */
            verified_at: string | null;
            /** Format: date-time */
            created_at: string | null;
        };
        Security: {
            identities: components["schemas"]["Identity"][];
            totp: {
                /**
                 * @description PENDING is set up but never confirmed.
                 * @enum {string}
                 */
                status: "ACTIVE" | "PENDING" | "NONE";
                /** Format: date-time */
                activated_at: string | null;
                /**
                 * Format: date-time
                 * @description The app's latest removal, by the user or an administrator; withdrawals wait for review for a day after it.
                 */
                changed_at: string | null;
            };
            /** Format: date-time */
            password_changed_at: string | null;
            /** Format: date-time */
            last_login_at: string | null;
            /** @description How long password sign-in stays locked after repeated failures; 0 when it is not. */
            locked_seconds: number;
            sessions: {
                /** Format: uuid */
                id: string;
                device_id: string;
                client_type: string;
                user_agent: string;
                /** @description Masked (203.0.113.*). */
                ip: string;
                /** Format: date-time */
                created_at: string | null;
                /** Format: date-time */
                last_seen_at: string | null;
            }[];
            devices: {
                device_id: string;
                /** Format: date-time */
                first_seen_at: string | null;
                /** Format: date-time */
                last_seen_at: string | null;
            }[];
            pending_identity_requests: number;
        };
        LoginEntry: {
            id: number;
            /** @description PASSWORD, OTP, LOGIN_CHALLENGE or REGISTER. */
            method: string;
            /** @description SUCCESS or a failure code. */
            result: string;
            identity_mask: string;
            device_id: string;
            user_agent: string;
            /** @description Masked. */
            ip: string;
            new_device: boolean;
            /** Format: date-time */
            created_at: string | null;
        };
        StatusChange: {
            from_status: string;
            to_status: string;
            reason_code: string;
            /** @description An administrator's email, cli:<os user> or a service. */
            actor: string;
            /** Format: date-time */
            at: string | null;
        };
        Consent: {
            /** @description TERMS or RISK_DISCLOSURE. */
            document: string;
            version: string;
            /** Format: date-time */
            accepted_at: string | null;
        };
        Assessment: {
            /** Format: uuid */
            id: string;
            /** @description The auth event assessed, e.g. auth.LoginSucceeded. */
            source_event_type: string;
            score: number;
            /** @enum {string} */
            action: "NONE" | "STEP_UP" | "REVIEW" | "REJECT";
            hits: {
                rule: string;
                score: number;
                detail: string;
            }[];
            /** @description Whether the action was carried out (risk.enforce). */
            enforced: boolean;
            /** Format: date-time */
            created_at: string | null;
        };
        ValuedBalance: {
            /** @enum {string} */
            account_type: "SPOT" | "FUTURES";
            asset: string;
            available: components["schemas"]["Decimal"];
            frozen: components["schemas"]["Decimal"];
            total: components["schemas"]["Decimal"];
            /** @description The total at the asset's USDT pair's last price; null without one. */
            value_usdt: components["schemas"]["NullableDecimal"];
        };
        /** @description An administrator's hold on part of a user's SPOT balance (ADMIN_FREEZE until released with ADMIN_UNFREEZE). */
        Hold: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            account_type: "SPOT";
            asset: string;
            amount: components["schemas"]["Decimal"];
            reason: string;
            /** @description The administrator who placed it. */
            actor: string;
            /** Format: uuid */
            journal_id: string;
            /** Format: date-time */
            created_at: string | null;
            active: boolean;
            /** Format: date-time */
            released_at: string | null;
            released_by: string;
            release_reason: string;
            release_journal_id: string | null;
        };
        /** @description An order as the service that holds it renders it. */
        ServiceOrder: {
            /** Format: uuid */
            order_id: string;
            status: string;
        } & {
            [key: string]: unknown;
        };
        /** @description A contract order as derivatives-service renders it. */
        ContractOrder: {
            /** Format: uuid */
            order_id: string;
            client_order_id: string;
            symbol: string;
            /** @enum {string} */
            side: "BUY" | "SELL";
            /** @enum {string} */
            position_side: "BOTH" | "LONG" | "SHORT";
            /** @enum {string} */
            type: "LIMIT" | "MARKET";
            price: components["schemas"]["Decimal"];
            quantity: components["schemas"]["Decimal"];
            reduce_only: boolean;
            status: string;
            filled_quantity: components["schemas"]["Decimal"];
            cancel_requested: boolean;
            /** Format: date-time */
            created_at: string;
        } & {
            [key: string]: unknown;
        };
        /** @description A contract position as derivatives-service renders it. */
        UserPosition: {
            /** Format: uuid */
            position_id: string;
            symbol: string;
            /** @enum {string} */
            position_side: "BOTH" | "LONG" | "SHORT";
            /** @description Signed, positive long. */
            quantity: components["schemas"]["Decimal"];
            entry_price: components["schemas"]["Decimal"];
            mark_price: components["schemas"]["NullableDecimal"];
            unrealized_pnl: components["schemas"]["NullableDecimal"];
            margin: components["schemas"]["Decimal"];
            /** @enum {string} */
            margin_mode: "CROSS" | "ISOLATED";
            leverage: number;
            liquidation_price: components["schemas"]["NullableDecimal"];
        } & {
            [key: string]: unknown;
        };
        IdentityRequest: {
            /** Format: uuid */
            id: string;
            /** Format: uuid */
            user_id: string;
            /** @enum {string} */
            kind: "EMAIL" | "PHONE";
            /** @description Masked. */
            new_value: string;
            /** @description Masked; empty when the identity is gone. */
            current_value: string;
            /** @enum {string} */
            status: "PENDING_REVIEW" | "APPROVED" | "REJECTED";
            /** Format: date-time */
            created_at: string | null;
            /** Format: date-time */
            decided_at: string | null;
            decided_by: string;
            reason: string;
        };
        UserView: {
            user: {
                /** Format: uuid */
                id: string;
                /** @enum {string} */
                status: "ACTIVE" | "RISK_REVIEW" | "FROZEN" | "CLOSED";
                region: string;
                language: string;
                kyc_level: number;
                /** Format: date-time */
                created_at: string;
            };
            balances: {
                /** @example SPOT */
                account_type: string;
                asset: string;
                available: components["schemas"]["Decimal"];
                frozen: components["schemas"]["Decimal"];
            }[];
        };
        Withdrawal: {
            /** Format: uuid */
            id: string;
            /**
             * Format: uuid
             * @description In lists only.
             */
            user_id?: string;
            asset: string;
            network: string;
            address: string;
            amount: components["schemas"]["Decimal"];
            fee: components["schemas"]["Decimal"];
            internal: boolean;
            /** @description The custodian sends it (ADR-0011). */
            custody?: boolean;
            status: string;
            /**
             * @description In lists only: the custodian's last word on it (SUBMITTED until
             *     it acknowledges, ACCEPTED, REVIEW, APPROVED, REJECTED, SUCCESS,
             *     FAILED); empty when the platform sends it.
             */
            provider_status?: string;
            /** Format: date-time */
            submitted_at?: string | null;
            /** @description In lists only. */
            risk_score?: number;
            risk_reasons: string[];
            value_usdt?: components["schemas"]["Decimal"];
            approvals_required: number;
            /** @description Reviewers so far (in lists only). */
            approvals?: string[];
            reject_reason?: string | null;
            tx_hash?: string | null;
            confirmations?: number;
            required_confirmations?: number;
            /** Format: date-time */
            created_at: string;
            approved_at?: string | null;
            broadcast_at?: string | null;
            confirmed_at?: string | null;
            /**
             * Format: date-time
             * @description Set while a reviewer has it on hold (held_by, with hold_note).
             */
            held_at?: string | null;
            held_by?: string;
            hold_note?: string;
            /**
             * Format: date-time
             * @description Only in a review's answer: the asset's withdrawals are suspended since then (funds missing on two custody checks, or an operator), so an approved one waits until an ADMIN resumes them (C5.5 ⑯).
             */
            suspended_at?: string;
            /** @description Only in a review's answer, with suspended_at. */
            suspension_reason?: string;
        };
        /** @description An asset whose withdrawals are suspended (wallet-service, C5.5 ⑯). */
        WithdrawalSuspension: {
            /** @example USDT */
            asset: string;
            /** @description What the custody checks found missing (0 for an operator's suspension). */
            shortfall: components["schemas"]["Decimal"];
            reason: string;
            /** @description wallet-service for the custody checks, else who suspended it. */
            suspended_by: string;
            /** Format: date-time */
            suspended_at: string;
        };
        WithdrawalDetail: {
            withdrawal: components["schemas"]["Withdrawal"];
            /** @description The address in the user's address book; null when it was deleted. */
            address_book: {
                label: string;
                /** Format: date-time */
                created_at: string;
                /**
                 * Format: date-time
                 * @description The end of its cooling-off period.
                 */
                usable_at: string;
            } | null;
            /** @description The user's withdrawals' worth today (UTC), refused ones left out. */
            used_today_usdt: components["schemas"]["Decimal"];
            /** @description The same this month. */
            used_month_usdt: components["schemas"]["Decimal"];
        };
        /** @description A deposit as wallet-service has it, with the admin console's decisions on it. */
        ReviewDeposit: {
            /** Format: uuid */
            id: string;
            /** Format: uuid */
            user_id: string;
            /** @enum {string} */
            kind: "CHAIN" | "INTERNAL";
            /** @description Null for a token no asset is configured for. */
            asset: string | null;
            network: string;
            address: string;
            tx_hash: string;
            amount: components["schemas"]["Decimal"];
            /** @enum {string} */
            status: "DETECTED" | "CONFIRMING" | "CONFIRMED" | "CREDITED" | "ORPHANED" | "REJECTED";
            /** @description Booked to UNCLAIMED_DEPOSIT instead of the user. */
            unclaimed: boolean;
            /**
             * @description UNKNOWN_ADDRESS for a deposit of nobody (user_id the nil UUID, B7a).
             * @enum {string|null}
             */
            reason: "BELOW_MINIMUM" | "ACCOUNT_CLOSED" | "NOT_ELIGIBLE" | "UNSUPPORTED_TOKEN" | "UNKNOWN_ADDRESS" | null;
            /** @description The custodian's trade (UDUN:<tradeId>). */
            trade_id: string | null;
            confirmations: number;
            required_confirmations: number;
            journal_id: string | null;
            /** Format: date-time */
            detected_at: string;
            confirmed_at: string | null;
            credited_at: string | null;
            /**
             * @description MANUAL for an administrator's backfill of a lost callback.
             * @enum {string}
             */
            source: "AUTO" | "MANUAL";
            entered_by: string;
            /** @description When the custodian's own callback matched a backfill. */
            callback_at: string | null;
            /** @description What in the custodian's callback disagreed with a backfill. */
            discrepancy: string;
            /** @description Waits for an administrator's decision. */
            attention: boolean;
            /** @enum {string} */
            resolution: "" | "CREDITED" | "DISMISSED";
            resolved_by: string;
            resolved_at: string | null;
            resolution_note: string;
            release_journal_id: string | null;
            /** @description For a deposit of nobody, the user its address belongs to now, or belonged to before it was retired (address_owner_retired): a hint for crediting it (POST /admin/v1/deposits/{id}/assign), never its owner. */
            address_owner?: string | null;
            address_owner_retired?: boolean;
        };
        /** @description The reference data in the shape of deploy/instruments/<env>.json. */
        InstrumentConfig: {
            fee_schedules: components["schemas"]["FeeScheduleConfig"][];
            assets: components["schemas"]["AssetConfig"][];
            pairs: components["schemas"]["PairConfig"][];
            contracts: components["schemas"]["ContractConfig"][];
        };
        /** @description Some items to create or change, each whole; the others are left alone. */
        InstrumentConfigPatch: {
            fee_schedules?: components["schemas"]["FeeScheduleConfig"][];
            assets?: components["schemas"]["AssetConfig"][];
            pairs?: components["schemas"]["PairConfig"][];
            contracts?: components["schemas"]["ContractConfig"][];
        };
        FeeScheduleConfig: {
            tier: string;
            maker_fee_rate: components["schemas"]["Decimal"];
            taker_fee_rate: components["schemas"]["Decimal"];
            version?: number;
        };
        AssetConfig: {
            asset_code: string;
            name: string;
            /** @description Fixed once set. */
            decimals: number;
            deposit_enabled: boolean;
            withdraw_enabled: boolean;
            trading_enabled: boolean;
            risk_restricted: boolean;
            rank?: number;
            categories?: string[];
            version?: number;
            /** @description The networks to create or change; the others are left alone. */
            networks?: components["schemas"]["NetworkConfig"][];
        };
        NetworkConfig: {
            asset_code?: string;
            network: string;
            chain: string;
            contract_address: string;
            confirmations: number;
            min_deposit: components["schemas"]["Decimal"];
            min_withdraw: components["schemas"]["Decimal"];
            withdraw_fee: components["schemas"]["Decimal"];
            memo_required: boolean;
            deposit_enabled: boolean;
            withdraw_enabled: boolean;
            display_name?: string;
            /** @enum {string} */
            address_format?: "EVM" | "TRON" | "BTC";
            eta_minutes?: number;
            explorer_tx_url?: string;
            explorer_address_url?: string;
            /** @description Empty for the platform's own wallet, UDUN for the custodian. */
            provider?: string;
            provider_coin?: string;
            version?: number;
        };
        PairConfig: {
            symbol: string;
            base_asset: string;
            quote_asset: string;
            tick_size: components["schemas"]["Decimal"];
            lot_size: components["schemas"]["Decimal"];
            min_quantity: components["schemas"]["Decimal"];
            max_quantity: components["schemas"]["Decimal"];
            min_notional: components["schemas"]["Decimal"];
            price_band: components["schemas"]["Decimal"];
            fee_tier: string;
            /**
             * @description Only when the pair is created (PREPARE when empty); later through the status endpoint.
             * @enum {string}
             */
            status: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
            /** @description The reference market's symbol (BTCUSDT); empty for none. */
            reference_symbol?: string;
            reference_multiplier: components["schemas"]["Decimal"];
            /** Format: date-time */
            listed_at?: string;
            version?: number;
        };
        ContractConfig: {
            symbol: string;
            /** @enum {string} */
            type: "PERPETUAL";
            base_asset: string;
            quote_asset: string;
            /** @description The BASE-QUOTE spot pair whose price is the index. */
            index_symbol: string;
            tick_size: components["schemas"]["Decimal"];
            lot_size: components["schemas"]["Decimal"];
            min_quantity: components["schemas"]["Decimal"];
            max_quantity: components["schemas"]["Decimal"];
            min_notional: components["schemas"]["Decimal"];
            price_band: components["schemas"]["Decimal"];
            /** @description 1-20 tiers, growing notionals, falling leverage (1-125), mmr below 1/leverage. */
            risk_tiers: components["schemas"]["RiskTier"][];
            funding_interval_hours: number;
            interest_rate: components["schemas"]["Decimal"];
            funding_cap: components["schemas"]["Decimal"];
            impact_notional: components["schemas"]["Decimal"];
            fee_tier: string;
            /** @enum {string} */
            status: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
            version?: number;
        };
        RiskTier: {
            max_notional: components["schemas"]["Decimal"];
            max_leverage: number;
            mmr: components["schemas"]["Decimal"];
        };
        ConfigResult: {
            changes: {
                /** @enum {string} */
                entity: "FEE_SCHEDULE" | "ASSET" | "NETWORK" | "TRADING_PAIR" | "CONTRACT";
                /** @description The tier, asset code, ASSET/NETWORK or symbol. */
                key: string;
                /** @enum {string} */
                action: "CREATE" | "UPDATE";
                version: number;
                /** @description The item before; null when created. */
                before: Record<string, never> | null;
                after: Record<string, never>;
            }[];
            unchanged: number;
            warnings: {
                /**
                 * @description REFERENCE_UNCHECKED: the reference market could not be asked (detail: the symbol);
                 *     HOUSE_NOT_LISTED: HOUSE quotes it only once on the flag's symbol list (detail: the flag);
                 *     HOUSE_QUOTES: HOUSE will quote it on the reference market's book (on the flag's symbol list, detail: the flag);
                 *     NO_INDEX_REFERENCE: the contract's index pair (detail) follows no reference market;
                 *     NO_FUTURES: no futures on the reference symbol (detail), so HOUSE gives the contract no book;
                 *     STREAMS_RECONNECT: the reference streams reconnect, books empty for about 20 seconds;
                 *     IMPACT_UNKNOWN: a new risk ladder (symbol) could not be measured against the open positions, so it cannot be confirmed now;
                 *     IMPACT_UNMEASURED: some of the contract's positions (detail: how many) have no fresh mark price or were too many to
                 *     measure in time, so the ladder cannot be confirmed now;
                 *     STATUS_IGNORED: the document gives an existing pair or contract another status (detail) than it has: a document never
                 *     moves one, a status change does.
                 * @enum {string}
                 */
                code: "REFERENCE_UNCHECKED" | "HOUSE_NOT_LISTED" | "HOUSE_QUOTES" | "NO_INDEX_REFERENCE" | "NO_FUTURES" | "STREAMS_RECONNECT" | "IMPACT_UNKNOWN" | "IMPACT_UNMEASURED" | "STATUS_IGNORED";
                symbol: string;
                detail: string;
            }[];
        };
        ReasonRequest: {
            reason: string;
        };
        /** @description A custodian deposit as the custodian's console shows it. */
        BackfillRequest: {
            /** @description A network whose custodian is not the chain itself (provider UDUN). */
            network: string;
            /** @description The custodian's trade ID as its console shows it (kept as <provider>:<tradeId>, like a callback's). */
            trade_id: string;
            /** @description The user's deposit address on the network. */
            address: string;
            tx_hash: string;
            amount: components["schemas"]["Decimal"];
        };
        Network: {
            asset_code?: string;
            network?: string;
            chain?: string;
            contract_address?: string;
            confirmations?: number;
            min_deposit?: components["schemas"]["Decimal"];
            min_withdraw?: components["schemas"]["Decimal"];
            withdraw_fee?: components["schemas"]["Decimal"];
            memo_required?: boolean;
            deposit_enabled?: boolean;
            withdraw_enabled?: boolean;
            version?: string;
        };
        Asset: {
            asset_code?: string;
            name?: string;
            decimals?: number;
            deposit_enabled?: boolean;
            withdraw_enabled?: boolean;
            trading_enabled?: boolean;
            risk_restricted?: boolean;
            networks?: components["schemas"]["Network"][];
            version?: string;
        };
        Pair: {
            symbol?: string;
            base_asset?: string;
            quote_asset?: string;
            tick_size?: components["schemas"]["Decimal"];
            lot_size?: components["schemas"]["Decimal"];
            min_quantity?: components["schemas"]["Decimal"];
            max_quantity?: components["schemas"]["Decimal"];
            min_notional?: components["schemas"]["Decimal"];
            price_band?: components["schemas"]["Decimal"];
            fee_tier?: string;
            maker_fee_rate?: components["schemas"]["Decimal"];
            taker_fee_rate?: components["schemas"]["Decimal"];
            /** @enum {string} */
            status?: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
            version?: string;
            /** @description The Binance symbol it follows (ADR-0010); empty when it follows none. */
            reference_symbol?: string;
            /** @description Platform price = reference price x multiplier (1000 for 1000SHIB, ADR-0014); empty for 1. */
            reference_multiplier?: string;
            /** Format: date-time */
            listed_at?: string;
        };
        Flag: {
            key: string;
            enabled: boolean;
            description: string;
            /** @description Targeting rules (regions, statuses, allow lists), kept as they are by a switch. */
            rules?: unknown;
            version: number;
            updated_by: string;
            /**
             * Format: date-time
             * @description Null for a known flag never set (off).
             */
            updated_at: string | null;
        };
        /** @description A fund operation (a manual adjustment, an insurance fund contribution or a deposit backfill). */
        Approval: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            kind: "LEDGER_ADJUSTMENT" | "INSURANCE_FUND" | "DEPOSIT_BACKFILL" | "SIM_EVENT" | "SIM_PARAMS" | "SIM_MINT" | "DEPOSIT_ASSIGN" | "WELCOME_CREDIT";
            /**
             * @description For LEDGER_ADJUSTMENT user_id, asset and amount, account_type FUTURES when not the SPOT account; for INSURANCE_FUND
             *     asset and amount; reference when given. For DEPOSIT_BACKFILL user_id, asset, amount, network, trade_id, address,
             *     tx_hash and entered_by (the backfill's result names the deposit, its journal_id stays null). For SIM_EVENT and
             *     SIM_PARAMS change (the event or {params} as JSON), actor (the requester, market-sim's actor) and move (the price move
             *     market-sim measured; volume for a turnover change); the result names the event or the settings' version. For
             *     SIM_MINT asset, amount (in all), bots (each bot's share as JSON: [{user_id, label, amount}]) and role when only
             *     one role's bots; its journal_id is the first bot's. For DEPOSIT_ASSIGN user_id (the user it is credited to),
             *     deposit_id, asset, amount, network, address, tx_hash, and former_holder (a retired address's holder) or
             *     address_owner (its holder now) when it has one; its journal_id is the release's.
             */
            payload: {
                [key: string]: string;
            };
            reason: string;
            /** @enum {string} */
            status: "PENDING" | "EXECUTED" | "REJECTED" | "FAILED";
            /**
             * Format: uuid
             * @description The requesting administrator's ID.
             */
            requested_by: string;
            requested_by_email: string;
            decided_by: string | null;
            decided_by_email: string | null;
            /**
             * @description The journal once executed, the ledger's refusal, or the rejection reason; while pending after an attempt
             *     (attempted_at), how the last attempt ended (a mint: "booked n of m; <bot>: <code>: <message>").
             */
            result: string;
            /** Format: date-time */
            created_at: string;
            decided_at: string | null;
            /**
             * @description SINGLE when its requester carries it out alone (single-person mode).
             * @enum {string}
             */
            mode: "TWO_PERSON" | "SINGLE";
            /** @description Its worth in USDT when requested; null without a price. */
            value_usdt: components["schemas"]["NullableDecimal"];
            /**
             * @description Why a two-person operation waits for a second administrator:
             *     asked for, two-person mode on, above the single-operation
             *     limit, over the 24-hour limit, or of unknown worth, or a
             *     simulated market's change beyond one operator's share, or a
             *     deposit of nobody credited to a user other than its address's
             *     holder; empty in single-person mode.
             * @enum {string}
             */
            escalation: "" | "REQUESTED" | "TWO_PERSON_MODE" | "SINGLE_LIMIT" | "DAILY_LIMIT" | "NO_PRICE" | "SIM_SHARE" | "NOT_ADDRESS_HOLDER" | "WELCOME_RAISE";
            journal_id: string | null;
            /**
             * Format: date-time
             * @description When an attempt to carry out a fund operation began (a single-person operation when requested, a two-person one
             *     when approved). PENDING with it, the attempt did not finish and may have booked: it is finished (approved again:
             *     its ledger key makes that safe), never rejected (ADMIN_APPROVAL_ATTEMPTED). Null for the simulated market's
             *     changes and before any attempt.
             */
            attempted_at: string | null;
            /**
             * @description In the list: a simulated market's pending request past its expiry (a day after it was asked for, or when its
             *     event was to start) by the server's clock; approving it only marks it FAILED. False otherwise.
             */
            expired?: boolean;
        };
        CrossMargin: {
            /** @example USDT */
            asset: string;
            /** @description The cross positions; with none there is nothing to liquidate. */
            positions: number;
            /** @description A cross position's contract has no fresh mark price; nothing else is measured. */
            unmeasured: boolean;
            equity: components["schemas"]["Decimal"];
            maintenance: components["schemas"]["Decimal"];
            /** @enum {string} */
            state: "HEALTHY" | "WARNING" | "LIQUIDATE";
            equity_after: components["schemas"]["Decimal"];
            /** @enum {string} */
            state_after: "HEALTHY" | "WARNING" | "LIQUIDATE";
        };
        AuditEntry: {
            /** Format: uuid */
            event_id: string;
            /** @example audit.v1.AdminActionPerformed */
            event_type: string;
            actor: string;
            target: string;
            /** Format: date-time */
            occurred_at: string;
            /** @description The event as JSON. */
            payload: unknown;
        };
        UsersBucket: {
            /** Format: date */
            day: string;
            registered: number;
            signed_in: number;
            traders: number;
            depositors: number;
            /** @description Every account registered by the bucket's end. */
            total: number;
        };
        HousePnLBucket: {
            /** Format: date */
            day: string;
            spot_pnl: components["schemas"]["Decimal"];
            contracts_pnl: components["schemas"]["Decimal"];
            funding: components["schemas"]["Decimal"];
            total: components["schemas"]["Decimal"];
            cumulative: components["schemas"]["Decimal"];
            spot_result: components["schemas"]["Decimal"];
        };
        TradingDay: {
            /** Format: date */
            day: string;
            symbol: string;
            trades: number;
            volume: components["schemas"]["Decimal"];
            quote_volume: components["schemas"]["Decimal"];
            /** @description Orders accepted. */
            orders: number;
            /** @description Orders rejected (by the funds check or the engine). */
            rejected: number;
        };
        WalletDay: {
            /** Format: date */
            day: string;
            asset: string;
            deposits: number;
            deposit_amount: components["schemas"]["Decimal"];
            withdrawals: number;
            withdrawal_amount: components["schemas"]["Decimal"];
            withdrawal_fees: components["schemas"]["Decimal"];
        };
        Candle: {
            /** Format: date-time */
            open_time: string;
            open: components["schemas"]["Decimal"];
            high: components["schemas"]["Decimal"];
            low: components["schemas"]["Decimal"];
            close: components["schemas"]["Decimal"];
            volume: components["schemas"]["Decimal"];
            quote_volume: components["schemas"]["Decimal"];
            trades: number;
        };
        NullableDecimal: string | null;
        Contract: {
            symbol?: string;
            /** @enum {string} */
            type?: "PERPETUAL";
            base_asset?: string;
            quote_asset?: string;
            index_symbol?: string;
            tick_size?: components["schemas"]["Decimal"];
            lot_size?: components["schemas"]["Decimal"];
            min_quantity?: components["schemas"]["Decimal"];
            max_quantity?: components["schemas"]["Decimal"];
            min_notional?: components["schemas"]["Decimal"];
            price_band?: components["schemas"]["Decimal"];
            risk_tiers?: {
                max_notional?: components["schemas"]["Decimal"];
                max_leverage?: number;
                mmr?: components["schemas"]["Decimal"];
            }[];
            funding_interval_hours?: number;
            interest_rate?: components["schemas"]["Decimal"];
            funding_cap?: components["schemas"]["Decimal"];
            impact_notional?: components["schemas"]["Decimal"];
            fee_tier?: string;
            maker_fee_rate?: components["schemas"]["Decimal"];
            taker_fee_rate?: components["schemas"]["Decimal"];
            /** @enum {string} */
            status?: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
            version?: string;
        };
        ContractState: {
            symbol: string;
            /** @enum {string} */
            status: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
            reduce_only: boolean;
            /** @description Why it last went reduce-only, e.g. MARK_PRICE_STALE or INDEX_SOURCES. */
            reduce_only_reason: string;
            /** Format: date-time */
            reduce_only_since: string | null;
            /** @description Who lifted the last reduce-only. */
            lifted_by: string;
            mark_price: components["schemas"]["NullableDecimal"];
            /** Format: date-time */
            mark_at: string | null;
            /** @description Whether the mark price is recent enough to trade on. */
            mark_fresh: boolean;
            /** @description The long quantity (equal to the short one). */
            open_interest: components["schemas"]["Decimal"];
            positions: number;
        };
        RiskPosition: {
            /** Format: uuid */
            position_id: string;
            /** Format: uuid */
            user_id: string;
            symbol: string;
            /** @enum {string} */
            position_side: "BOTH" | "LONG" | "SHORT";
            /** @description Signed: positive long, negative short. */
            quantity: components["schemas"]["Decimal"];
            entry_price: components["schemas"]["Decimal"];
            mark_price: components["schemas"]["NullableDecimal"];
            notional: components["schemas"]["NullableDecimal"];
            unrealized_pnl: components["schemas"]["NullableDecimal"];
            margin: components["schemas"]["Decimal"];
            /** @enum {string} */
            margin_mode: "CROSS" | "ISOLATED";
            leverage: number;
            maintenance_margin: components["schemas"]["NullableDecimal"];
            liquidation_price: components["schemas"]["NullableDecimal"];
            realized_pnl: components["schemas"]["Decimal"];
            funding: components["schemas"]["Decimal"];
            /** Format: date-time */
            updated_at: string;
            /** @description Taken over by the liquidation engine. */
            liquidating: boolean;
            liquidation_attempts: number;
            /**
             * Format: date-time
             * @description When the position was warned; a cross position's is its account's (the margin monitor warns the cross account).
             */
            warned_at: string | null;
            /** @description Maintenance margin / (margin + unrealized result); null without a mark price or once the margin is gone. */
            margin_ratio: components["schemas"]["NullableDecimal"];
            /** @description False while the mark price is older than the margin monitor accepts; the figures stand still until it moves. */
            mark_fresh: boolean;
        };
        LiquidationStep: {
            /** Format: uuid */
            event_id: string;
            /**
             * @description WARNING: margin balance at most 1.2 × maintenance; STARTED:
             *     taken over; FILLED: a liquidation order (or, adl, an
             *     auto-deleveraging) closed part of it; ADL: a counterparty's
             *     position closed by auto-deleveraging.
             * @enum {string}
             */
            kind: "WARNING" | "STARTED" | "FILLED" | "ADL";
            /** Format: uuid */
            user_id: string;
            /** @description Empty for a warning of a cross account. */
            symbol: string;
            position_side: string;
            cross: boolean;
            adl: boolean;
            trade_id: string;
            price: components["schemas"]["Decimal"];
            quantity: components["schemas"]["Decimal"];
            realized_pnl: components["schemas"]["Decimal"];
            insurance_paid: components["schemas"]["Decimal"];
            mark_price: components["schemas"]["Decimal"];
            bankruptcy_price: components["schemas"]["Decimal"];
            margin_balance: components["schemas"]["Decimal"];
            maintenance_margin: components["schemas"]["Decimal"];
            /** Format: date-time */
            occurred_at: string;
        };
        InsuranceFund: {
            asset: string;
            balance: components["schemas"]["Decimal"];
            pnl_clearing: components["schemas"]["Decimal"];
        };
        DerivativesDay: {
            /** Format: date */
            day: string;
            symbol: string;
            /** @description Settled sides of trades (two per trade). */
            fills: number;
            /** @description Quantity traded (once per trade). */
            volume: components["schemas"]["Decimal"];
            notional: components["schemas"]["Decimal"];
            fees: components["schemas"]["Decimal"];
            realized_pnl: components["schemas"]["Decimal"];
            funding_paid: components["schemas"]["Decimal"];
            funding_received: components["schemas"]["Decimal"];
            /** @description Positions taken over. */
            liquidations: number;
            /** @description Counterparty positions auto-deleveraged. */
            adl: number;
            insurance_paid: components["schemas"]["Decimal"];
        };
        OpenInterest: {
            symbol: string;
            long: components["schemas"]["Decimal"];
            short: components["schemas"]["Decimal"];
            positions: number;
        };
        CustodyOverview: {
            /** @example UDUN */
            provider: string;
            configured: boolean;
            /** @description Why the custodian's coins could not be read. */
            error: string | null;
            coins: components["schemas"]["CustodyCoin"][];
            checks: components["schemas"]["ChainCheck"][];
            /** @description Withdrawals with the custodian. */
            submitted: {
                count: number;
                amount_usdt: components["schemas"]["Decimal"];
                /** Format: date-time */
                oldest_at: string | null;
            };
            callbacks: {
                /** @description Verified callbacks that FAILED or stayed UNMATCHED. */
                attention: number;
                /** Format: date-time */
                last_at: string | null;
            };
        };
        CustodyCoin: {
            /**
             * @description The custodian's code, mainCoinType:coinType.
             * @example 195:TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t
             */
            coin: string;
            symbol: string;
            decimals: number;
            /** @description What the custodian holds, as it reports it. */
            balance: string | null;
            token: boolean;
            /** @description The asset and network rows that use the coin; empty for a coin no network uses. */
            networks: {
                asset: string;
                network: string;
            }[];
        };
        /**
         * @description Invariant 4 for one holder of an asset: shortfall = expected −
         *     held − elsewhere − in_flight − unbooked; above zero the holders
         *     miss funds.
         */
        ChainCheck: {
            /** @description The custodian (UDUN, UDUNMOCK), or the network (e.g. ETH-SEPOLIA) for the platform's own wallets. */
            holder: string;
            asset: string;
            held: components["schemas"]["Decimal"];
            expected: components["schemas"]["Decimal"];
            elsewhere: components["schemas"]["Decimal"];
            in_flight: components["schemas"]["Decimal"];
            unbooked: components["schemas"]["Decimal"];
            shortfall: components["schemas"]["Decimal"];
            /**
             * @description Simulated deposits of the custodian's stand-in, taken out of
             *     expected when the real gateway replaced it: they are at no
             *     custodian (zero before the switch).
             */
            baseline?: components["schemas"]["Decimal"];
            addresses: number;
            /** Format: date-time */
            checked_at: string;
        };
        CustodyFee: {
            /** Format: uuid */
            withdrawal_id: string;
            /** @description The custodian that charged it. */
            provider?: string;
            /** @description The custodian and its trade, which key the fee. */
            tx_hash: string;
            asset: string;
            network: string;
            amount: components["schemas"]["Decimal"];
            /**
             * @description How the custodian counts its fee on the network, as a person confirmed it; null while nobody did.
             * @enum {string|null}
             */
            unit: "SELF" | "MAIN" | "OUTSIDE" | null;
            /** @enum {string} */
            status: "HELD" | "BOOKABLE" | "WRITTEN_OFF";
            hold_reason: string;
            journal_id: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            booked_at: string | null;
            /** Format: date-time */
            written_off_at: string | null;
            /** @description Who booked or wrote it off (an administrator's email, or exchangectl's operator). */
            resolved_by: string;
            /** @description Their reason. */
            resolution: string;
        };
        CustodyCallback: {
            /** Format: uuid */
            id: string;
            provider: string;
            trade_id: string;
            /** @enum {string} */
            kind: "" | "DEPOSIT" | "WITHDRAWAL";
            /** @description The custodian's status (0 review, 1 approved, 2 refused, 3 success, 4 failed). */
            status: number | null;
            /** @description The withdrawal ID of a withdrawal. */
            business_id: string;
            coin: string;
            address: string;
            amount: components["schemas"]["Decimal"] | null;
            tx_hash: string;
            signature_ok: boolean;
            /** @enum {string} */
            result: "RECEIVED" | "APPLIED" | "IGNORED" | "UNMATCHED" | "REJECTED" | "FAILED" | "DISCREPANCY";
            detail: string;
            attempts: number;
            /** Format: date-time */
            received_at: string;
            /** Format: date-time */
            processed_at: string | null;
            /** @description The addresses it came from, the latest 8 (a repeat from a new address is added). */
            remote_ips?: string[];
            /** @description The request as received (one callback only). */
            raw?: string;
        };
        Error: {
            /**
             * @description Stable machine-readable code (appendix C), used by clients for i18n.
             * @example COMMON_INVALID_ARGUMENT
             */
            code: string;
            /** @description Human-readable explanation, safe to show. */
            message: string;
            trace_id: string;
            details?: {
                [key: string]: unknown;
            };
        };
    };
    responses: {
        /** @description Error in the unified structure. */
        Error: {
            headers: {
                "X-Trace-Id": components["headers"]["X-Trace-Id"];
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error"];
            };
        };
    };
    parameters: {
        /**
         * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
         *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
         *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
         *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
         *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
         */
        IdempotencyKey: string;
        /** @description The withdrawal the custodian charged the fee on. */
        FeeWithdrawalID: string;
        CallbackID: string;
        /** @description The previous page's next_cursor; omitted for the first page. */
        Cursor: string;
        Limit: number;
        /** @description From this time on (RFC 3339). */
        From: string;
        /** @description Before this time (RFC 3339). */
        To: string;
        UserFilter: string;
        /** @description Keeps the simulated market's bots (bots) or everyone else (users); all when absent. Needs market-sim's list of bots (503 COMMON_UNAVAILABLE without it); without the filter the rows are only marked, and stay unmarked while market-sim does not answer. */
        Accounts: "bots" | "users";
        /** @description Days back, today included. */
        Days: number;
        /**
         * @description The period's first day (UTC), instead of days; to defaults to
         *     today. At most a year by day, three years by week or month.
         */
        ReportFrom: string;
        /** @description The period's last day (UTC), included; needs from. */
        ReportTo: string;
        /** @description The rows' span; a row's day is its bucket's first (a week starts on Monday). */
        ReportBucket: "day" | "week" | "month";
        UserID: string;
        AdminID: string;
        OrderID: string;
        Contract: string;
        Symbol: string;
        ChangeID: string;
        ArticleID: string;
    };
    requestBodies: never;
    headers: {
        /** @description W3C trace ID of the request, shared by logs and events. */
        "X-Trace-Id": string;
    };
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    loginOptions: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The sign-in options. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        totp_required: boolean;
                    };
                };
            };
        };
    };
    login: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** Format: email */
                    email: string;
                    password: string;
                    /** @description Required unless GET /admin/v1/login-options says otherwise. */
                    totp_code?: string;
                };
            };
        };
        responses: {
            /** @description Signed in. */
            200: {
                headers: {
                    /** @description admin_session=...; Path=/admin/; HttpOnly; Secure; SameSite=Strict */
                    "Set-Cookie"?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        admin: components["schemas"]["Admin"];
                        /** Format: date-time */
                        expires_at: string;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    logout: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Signed out; the cookie is cleared. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    me: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The administrator. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Admin"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    changeOwnPassword: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    current_password: string;
                    new_password: string;
                };
            };
        };
        responses: {
            /** @description Changed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    startOwnTOTP: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    current_password: string;
                    /** @description The current authenticator's code; left out while admin.login_without_totp is on. */
                    totp_code?: string;
                };
            };
        };
        responses: {
            /** @description The new authenticator. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        totp_secret: string;
                        totp_uri: string;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    confirmOwnTOTP: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    totp_code: string;
                };
            };
        };
        responses: {
            /** @description Bound. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    inspectAdminSetup: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    token: string;
                };
            };
        };
        responses: {
            /** @description The setup. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        email: string;
                        name: string;
                        /** @enum {string} */
                        kind: "CREATE" | "PASSWORD" | "TOTP";
                        /** Format: date-time */
                        expires_at: string;
                        sets_password: boolean;
                        totp_secret: string | null;
                        totp_uri: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    completeAdminSetup: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    token: string;
                    password?: string;
                    totp_code?: string;
                };
            };
        };
        responses: {
            /** @description Set up. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    listRoles: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The roles. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        roles: components["schemas"]["RolePermissions"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listAdmins: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The administrators. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        admins: components["schemas"]["ManagedAdmin"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    createAdmin: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** Format: email */
                    email: string;
                    name: string;
                    role: components["schemas"]["AdminRole"];
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The administrator and their setup link. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AdminSetup"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    setAdminStatus: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["AdminID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    enabled: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The administrator. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ManagedAdmin"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    setAdminRole: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["AdminID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    role: components["schemas"]["AdminRole"];
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The administrator. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ManagedAdmin"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    resetAdminPassword: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["AdminID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            /** @description The setup link. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AdminSetup"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    resetAdminTOTP: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["AdminID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            /** @description The setup link. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AdminSetup"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    adminSessions: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["AdminID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The sessions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        sessions: components["schemas"]["AdminSession"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    revokeAdminSessions: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["AdminID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            /** @description The sessions ended. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    getSettings: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The settings. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Settings"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    updateSettings: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    two_person_approval?: boolean;
                    single_max_usdt?: components["schemas"]["Decimal"];
                    daily_max_usdt?: components["schemas"]["Decimal"];
                    withdrawal_max_usdt?: components["schemas"]["Decimal"];
                    /** @description At least change_delay_floor_seconds (COMMON_INVALID_ARGUMENT below it). */
                    change_delay_seconds?: number;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The settings after the change. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Settings"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getTodo: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The counts. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Todo"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    streamEvents: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The stream. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/event-stream": string;
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listUsers: {
        parameters: {
            query?: {
                status?: "ACTIVE" | "RISK_REVIEW" | "FROZEN" | "CLOSED";
                /** @description ISO 3166-1 alpha-2. */
                region?: string;
                /** @description From this time on (RFC 3339). */
                from?: components["parameters"]["From"];
                /** @description Before this time (RFC 3339). */
                to?: components["parameters"]["To"];
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of accounts. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["UserSummary"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listOrders: {
        parameters: {
            query?: {
                user_id?: components["parameters"]["UserFilter"];
                /** @description One order (the console's search). */
                order_id?: string;
                symbol?: string;
                status?: "NEW" | "OPEN" | "PARTIALLY_FILLED" | "FILLED" | "CANCELED" | "REJECTED";
                side?: "BUY" | "SELL";
                /** @description Keeps the simulated market's bots (bots) or everyone else (users); all when absent. Needs market-sim's list of bots (503 COMMON_UNAVAILABLE without it); without the filter the rows are only marked, and stay unmarked while market-sim does not answer. */
                accounts?: components["parameters"]["Accounts"];
                /** @description From this time on (RFC 3339). */
                from?: components["parameters"]["From"];
                /** @description Before this time (RFC 3339). */
                to?: components["parameters"]["To"];
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of orders. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Order"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listTrades: {
        parameters: {
            query?: {
                user_id?: components["parameters"]["UserFilter"];
                symbol?: string;
                /** @description Keeps the simulated market's bots (bots) or everyone else (users); all when absent. Needs market-sim's list of bots (503 COMMON_UNAVAILABLE without it); without the filter the rows are only marked, and stay unmarked while market-sim does not answer. */
                accounts?: components["parameters"]["Accounts"];
                /** @description From this time on (RFC 3339). */
                from?: components["parameters"]["From"];
                /** @description Before this time (RFC 3339). */
                to?: components["parameters"]["To"];
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of trades. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Trade"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listDeposits: {
        parameters: {
            query?: {
                user_id?: components["parameters"]["UserFilter"];
                asset?: string;
                network?: string;
                status?: "DETECTED" | "CONFIRMING" | "CONFIRMED" | "CREDITED" | "ORPHANED" | "REJECTED";
                /** @description The deposits of one transaction, any letter case (the console's search). */
                tx_hash?: string;
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of deposits. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Deposit"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listReviewDeposits: {
        parameters: {
            query?: {
                user_id?: components["parameters"]["UserFilter"];
                status?: "DETECTED" | "CONFIRMING" | "CONFIRMED" | "CREDITED" | "ORPHANED" | "REJECTED";
                network?: string;
                attention?: "true";
                manual_pending?: "true";
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of deposits. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["ReviewDeposit"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    checkDepositBackfill: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["BackfillRequest"];
            };
        };
        responses: {
            /** @description What the backfill would book. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        /** Format: uuid */
                        user_id: string;
                        asset: string;
                        /** @description Below the network's minimum, so booked to UNCLAIMED_DEPOSIT instead of the user. */
                        unclaimed: boolean;
                        /** @description The amount's worth; null without a price (a second administrator then decides). */
                        value_usdt: components["schemas"]["NullableDecimal"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    backfillDeposit: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["BackfillRequest"] & {
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The fund operation, executed or waiting for a second administrator. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getReviewDeposit: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The deposit. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReviewDeposit"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    creditDeposit: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            /** @description The deposit, CREDITED. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReviewDeposit"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    assignDeposit: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /**
                     * Format: uuid
                     * @description The user it is credited to.
                     */
                    user_id: string;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The operation, EXECUTED with the release's journal, or PENDING with why it waits. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    rejectDeposit: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            /** @description The deposit. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ReviewDeposit"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getDashboard: {
        parameters: {
            query?: {
                /** @description Days back, today included. */
                days?: components["parameters"]["Days"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The overview. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Dashboard"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    lookupUser: {
        parameters: {
            query: {
                q: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The account and its balances. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserView"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    changeUserStatus: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @enum {string} */
                    to: "ACTIVE" | "RISK_REVIEW" | "FROZEN" | "CLOSED";
                    /** @description Reason code of user-service, e.g. SUSPICIOUS_LOGIN, USER_REQUEST. */
                    reason: string;
                    note?: string;
                };
            };
        };
        responses: {
            /** @description The change. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Transition"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getUser: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The account. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["UserSummary"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listUserNotes: {
        parameters: {
            query?: {
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of notes. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Note"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    addUserNote: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    body: string;
                };
            };
        };
        responses: {
            /** @description The note. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Note"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    setUserTags: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    tags: string[];
                };
            };
        };
        responses: {
            /** @description The tags as stored (upper case, sorted). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        tags: string[];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    adjustUserBalance: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /**
                     * @default SPOT
                     * @enum {string}
                     */
                    account_type?: "SPOT" | "FUTURES";
                    /** @example USDT */
                    asset: string;
                    /** @description Positive credits, negative debits. */
                    amount: components["schemas"]["Decimal"];
                    reason: string;
                    /** @description A ticket or order number kept with it (in the journal's memo). */
                    reference?: string;
                };
            };
        };
        responses: {
            /** @description The operation. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    cancelUserOrders: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description Cancellations requested. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    getUserSecurity: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The account's security. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Security"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    revealUserContacts: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The identities. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        identities: components["schemas"]["Identity"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listUserLogins: {
        parameters: {
            query?: {
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of sign-ins. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["LoginEntry"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    revokeUserSessions: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /**
                     * Format: uuid
                     * @description Omitted for every live session.
                     */
                    session_id?: string;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description How many sessions ended. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        revoked: number;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    resetUserTotp: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description Whether there was one to remove. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        removed: boolean;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    resetUserPassword: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The temporary password (not cached). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        temporary_password: string;
                        sessions_revoked: number;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getUserHistory: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The history. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        status_changes: components["schemas"]["StatusChange"][];
                        consents: components["schemas"]["Consent"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getUserRisk: {
        parameters: {
            query?: {
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The assessments. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        assessments: components["schemas"]["Assessment"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listIdentityRequests: {
        parameters: {
            query?: {
                status?: "PENDING_REVIEW" | "APPROVED" | "REJECTED";
                user_id?: components["parameters"]["UserFilter"];
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of requests. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["IdentityRequest"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    decideIdentityRequest: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    approve: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The decided request. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["IdentityRequest"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getUserBalances: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The balances. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        balances: components["schemas"]["ValuedBalance"][];
                        total_usdt: components["schemas"]["Decimal"];
                        unpriced: string[];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listUserHolds: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The holds. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        holds: components["schemas"]["Hold"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    placeUserHold: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @example USDT */
                    asset: string;
                    /** @description Positive. */
                    amount: components["schemas"]["Decimal"];
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The hold. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Hold"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    releaseUserHold: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path: {
                id: components["parameters"]["UserID"];
                hold: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The released hold. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Hold"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    cancelUserOrder: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
                order: components["parameters"]["OrderID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description Cancel requested. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ServiceOrder"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listUserContractOrders: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The orders. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["ContractOrder"][];
                        next_cursor?: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    cancelUserContractOrder: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
                order: components["parameters"]["OrderID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description Cancel requested. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContractOrder"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listUserPositions: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The positions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        positions: components["schemas"]["UserPosition"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getUserFuturesMargin: {
        parameters: {
            query?: {
                /** @description The debit to measure (positive; 0 by default). */
                debit?: components["schemas"]["Decimal"];
            };
            header?: never;
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The cross margin. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CrossMargin"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    closeUserPosition: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path: {
                id: components["parameters"]["UserID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @example BTC-USDT-PERP */
                    symbol: string;
                    /** @enum {string} */
                    position_side: "BOTH" | "LONG" | "SHORT";
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The closing order. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContractOrder"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listWithdrawals: {
        parameters: {
            query?: {
                /** @description A withdrawal status (appendix B), e.g. APPROVED, SUBMITTED, BROADCAST, CONFIRMED, REJECTED, FAILED; ALL for every status. */
                status?: string;
                user_id?: components["parameters"]["UserFilter"];
                asset?: string;
                /** @description One network, e.g. TRON or ETH-SEPOLIA; every network when empty. */
                network?: string;
                order?: "asc" | "desc";
                /** @description true for the withdrawals on hold (in review), false for the others. */
                held?: "true" | "false";
                min_value_usdt?: components["schemas"]["Decimal"];
                max_value_usdt?: components["schemas"]["Decimal"];
                /** @description The lowest risk score listed. */
                min_risk?: number;
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of withdrawals. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Withdrawal"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    reviewWithdrawal: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    approve: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The withdrawal after the review. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Withdrawal"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    reviewWithdrawalBatch: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    ids: string[];
                    approve: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description What came of each. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        results: {
                            id: string;
                            ok: boolean;
                            /** @description The withdrawal's status after the review. */
                            status?: string;
                            /** @description The error code of a failed one. */
                            code?: string;
                            message?: string;
                            /** @description Approved, it waits until its asset's withdrawals are resumed. */
                            suspended?: boolean;
                        }[];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listWithdrawalSuspensions: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The suspended assets. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["WithdrawalSuspension"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    resumeWithdrawals: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                asset: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            /** @description The suspension lifted. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WithdrawalSuspension"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getWithdrawal: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The withdrawal. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WithdrawalDetail"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    holdWithdrawal: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    hold: boolean;
                    /** @description Required to put it on hold. */
                    note?: string;
                };
            };
        };
        responses: {
            /** @description The withdrawal. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Withdrawal"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listInstruments: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The reference data. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        assets: components["schemas"]["Asset"][];
                        pairs: components["schemas"]["Pair"][];
                        contracts: components["schemas"]["Contract"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getInstrumentConfig: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The document. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InstrumentConfig"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    previewInstrumentConfig: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    config: components["schemas"]["InstrumentConfigPatch"];
                };
            };
        };
        responses: {
            /** @description The changes it would make. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConfigResult"] & {
                        guard: components["schemas"]["ChangeGuard"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    applyInstrumentConfig: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    config: components["schemas"]["InstrumentConfigPatch"];
                    reason: string;
                    /** @description The preview's guard.confirmation.token, for a document moving trading parameters. */
                    confirmation?: string;
                };
            };
        };
        responses: {
            /** @description The changes made. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConfigResult"] & {
                        change: null;
                    };
                };
            };
            /** @description The changes confirmed, waiting. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConfigResult"] & {
                        change: components["schemas"]["InstrumentChange"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listArticles: {
        parameters: {
            query: {
                section: "ANNOUNCEMENT" | "HELP" | "LEGAL" | "HOME";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The articles. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        articles: components["schemas"]["ContentArticle"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    createArticle: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ArticleWrite"] & {
                    /** @enum {string} */
                    section: "ANNOUNCEMENT" | "HELP" | "LEGAL" | "HOME";
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The draft. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContentArticle"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getArticle: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ArticleID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The article. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContentArticle"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    updateArticle: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ArticleID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ArticleWrite"] & {
                    version: number;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The article. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContentArticle"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    publishArticle: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ArticleID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    version: number;
                    /** Format: date-time */
                    publish_at?: string;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The article. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContentArticle"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    archiveArticle: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ArticleID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    version: number;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The article. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContentArticle"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listBroadcasts: {
        parameters: {
            query?: {
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of messages. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Broadcast"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    sendBroadcast: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @enum {string} */
                    audience: "ALL" | "USER" | "TAG";
                    /**
                     * Format: uuid
                     * @description With USER.
                     */
                    user_id?: string;
                    /** @description With TAG. */
                    tag?: string;
                    title: components["schemas"]["LocalizedText"];
                    body: components["schemas"]["LocalizedText"];
                    /** @description A path on the sites the message leads to, such as /assets. */
                    link?: string;
                    email?: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The message, being delivered. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Broadcast"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getBroadcast: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The message. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Broadcast"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    resumeBroadcast: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            /** @description The message, sending again. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Broadcast"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getSim: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The state. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SimStatus"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getSimHistory: {
        parameters: {
            query?: {
                minutes?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The samples, oldest first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["SimSample"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listSimEvents: {
        parameters: {
            query?: {
                all?: boolean;
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The events. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["SimEvent"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    createSimEvent: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["SimEventWrite"] & components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The event. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        event: components["schemas"]["SimEvent"];
                    };
                };
            };
            /** @description Beyond one operator's share; waiting for a second administrator. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        approval: components["schemas"]["Approval"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    endSimEvent: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The event, ended. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SimEvent"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getSimPlan: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The plan. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SimPlan"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    previewSimTarget: {
        parameters: {
            query: {
                /** @description The level. */
                price: string;
                /** @description The window, at most a day (market-sim's longest). */
                duration_seconds: number;
                /** @description ABOVE or BELOW; inferred from the current target when absent. */
                direction?: "ABOVE" | "BELOW";
                /** @description When it starts, at most 24 hours ahead (else 400 ADMIN_SIM_TOO_FAR_AHEAD); now when absent. */
                starts_at?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The preview. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SimTargetPreview"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    updateSimParams: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    params: components["schemas"]["SimParams"];
                } & components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The settings' new version. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        version: number;
                    };
                };
            };
            /** @description Waiting for a second administrator. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        approval: components["schemas"]["Approval"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    simImpact: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    price: components["schemas"]["Decimal"];
                };
            };
        };
        responses: {
            /** @description The impact. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SimImpact"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getSimToken: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The holdings. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SimToken"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    mintSimBots: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /**
                     * @description The coin or USDT.
                     * @example ASTRA
                     */
                    asset: string;
                    /** @description In all, positive. */
                    amount: components["schemas"]["Decimal"];
                    /**
                     * @description Only the bots of this role; every bot when absent.
                     * @enum {string}
                     */
                    role?: "MAKER" | "TAKER" | "TREND" | "EXECUTOR";
                    reason: string;
                    reference?: string;
                };
            };
        };
        responses: {
            /** @description The operation (payload bots lists each bot's share as JSON). */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getPlatformProfile: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The profile. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformProfileAdmin"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    updatePlatformProfile: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PlatformProfileWrite"] & components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The profile as saved. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformProfileAdmin"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    uploadPlatformImage: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kind: "logo_light" | "logo_dark" | "favicon" | "apple_touch_icon";
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @description The image, base64. */
                    data: string;
                    /** @enum {string} */
                    mime: "image/png" | "image/svg+xml" | "image/webp";
                } & components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The profile with the image's new URL. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformProfileAdmin"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    deletePlatformImage: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                kind: "logo_light" | "logo_dark" | "favicon" | "apple_touch_icon";
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The profile without the image. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformProfileAdmin"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getWelcomeCredits: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The setting. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WelcomeCreditsSetting"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    setWelcomeCredits: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    credits: components["schemas"]["WelcomeCredit"][];
                    /** Format: int64 */
                    expected_version: number;
                } & components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description Lowered or cleared, at once. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WelcomeCreditsSetting"];
                };
            };
            /** @description A raise waits for a second ADMIN. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        approval: components["schemas"]["Approval"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getLaunchChecklist: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The checklist. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["LaunchChecklist"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getAssetProfile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                code: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The profile. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssetProfile"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    updateAssetProfile: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                code: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["AssetProfileWrite"] & components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The profile, its version moved on. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["AssetProfile"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listInstrumentChanges: {
        parameters: {
            query?: {
                status?: "PENDING_APPROVAL" | "SCHEDULED" | "APPLIED" | "CANCELED" | "REJECTED" | "FAILED";
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of changes. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["InstrumentChange"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    decideInstrumentChange: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ChangeID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    approve: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The change. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InstrumentChange"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    cancelInstrumentChange: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["ChangeID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["ReasonRequest"];
            };
        };
        responses: {
            /** @description The change. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InstrumentChange"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    previewPairStatus: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                symbol: components["parameters"]["Symbol"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @enum {string} */
                    to: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
                };
            };
        };
        responses: {
            /** @description The move. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StatusPreview"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    setPairStatus: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                symbol: components["parameters"]["Symbol"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["StatusRequest"];
            };
        };
        responses: {
            /** @description Halted. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StatusResult"];
                };
            };
            /** @description The change confirmed, waiting. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StatusResult"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listFlags: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The flags. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Flag"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    switchFlag: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                key: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    enabled: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The flag after the change. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Flag"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    requestAdjustment: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** Format: uuid */
                    user_id: string;
                    /**
                     * @default SPOT
                     * @enum {string}
                     */
                    account_type?: "SPOT" | "FUTURES";
                    /** @example USDT */
                    asset: string;
                    amount: components["schemas"]["Decimal"];
                    reason: string;
                    reference?: string;
                    /**
                     * @description Carry it out at once when single-person mode and its limits allow.
                     * @default false
                     */
                    direct?: boolean;
                };
            };
        };
        responses: {
            /** @description The request. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listApprovals: {
        parameters: {
            query?: {
                /** @description Empty for all. */
                status?: "PENDING" | "EXECUTED" | "REJECTED" | "FAILED";
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of requests. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Approval"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    simApprovalPreview: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The request measured now. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["SimPreview"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    decideApproval: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    approve: boolean;
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The decided request. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    searchAuditLogs: {
        parameters: {
            query?: {
                /** @description Exact actor, e.g. an administrator's email or cli:ubuntu. */
                actor?: string;
                /** @description Exact target, e.g. user:<id>, pair:BTC-USDT, flag:wallet.withdraw. */
                target?: string;
                /** @description Exact event type, e.g. exchange.audit.v1.AdminAction. */
                event_type?: string;
                /** @description From this time on (RFC 3339). */
                from?: components["parameters"]["From"];
                /** @description Before this time (RFC 3339). */
                to?: components["parameters"]["To"];
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of entries. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["AuditEntry"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    exportAuditLogs: {
        parameters: {
            query?: {
                actor?: string;
                target?: string;
                event_type?: string;
                /** @description From this time on (RFC 3339). */
                from?: components["parameters"]["From"];
                /** @description Before this time (RFC 3339). */
                to?: components["parameters"]["To"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The CSV file (Content-Disposition attachment). */
            200: {
                headers: {
                    /** @description More entries matched than were exported. */
                    "X-Truncated"?: "true" | "false";
                    [name: string]: unknown;
                };
                content: {
                    "text/csv": string;
                };
            };
            default: components["responses"]["Error"];
        };
    };
    tradingReport: {
        parameters: {
            query?: {
                /** @description Days back, today included. */
                days?: components["parameters"]["Days"];
                /**
                 * @description The period's first day (UTC), instead of days; to defaults to
                 *     today. At most a year by day, three years by week or month.
                 */
                from?: components["parameters"]["ReportFrom"];
                /** @description The period's last day (UTC), included; needs from. */
                to?: components["parameters"]["ReportTo"];
                /** @description The rows' span; a row's day is its bucket's first (a week starts on Monday). */
                bucket?: components["parameters"]["ReportBucket"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The days. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["TradingDay"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    walletReport: {
        parameters: {
            query?: {
                /** @description Days back, today included. */
                days?: components["parameters"]["Days"];
                /**
                 * @description The period's first day (UTC), instead of days; to defaults to
                 *     today. At most a year by day, three years by week or month.
                 */
                from?: components["parameters"]["ReportFrom"];
                /** @description The period's last day (UTC), included; needs from. */
                to?: components["parameters"]["ReportTo"];
                /** @description The rows' span; a row's day is its bucket's first (a week starts on Monday). */
                bucket?: components["parameters"]["ReportBucket"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The days. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["WalletDay"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    candleReport: {
        parameters: {
            query: {
                symbol: string;
                interval?: "1m" | "5m" | "15m" | "1h" | "4h" | "1d";
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The candles. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Candle"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    derivativesReport: {
        parameters: {
            query?: {
                /** @description Days back, today included. */
                days?: components["parameters"]["Days"];
                /**
                 * @description The period's first day (UTC), instead of days; to defaults to
                 *     today. At most a year by day, three years by week or month.
                 */
                from?: components["parameters"]["ReportFrom"];
                /** @description The period's last day (UTC), included; needs from. */
                to?: components["parameters"]["ReportTo"];
                /** @description The rows' span; a row's day is its bucket's first (a week starts on Monday). */
                bucket?: components["parameters"]["ReportBucket"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The days. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["DerivativesDay"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    openInterestReport: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The contracts with open positions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["OpenInterest"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    usersReport: {
        parameters: {
            query?: {
                /** @description Days back, today included. */
                days?: components["parameters"]["Days"];
                /**
                 * @description The period's first day (UTC), instead of days; to defaults to
                 *     today. At most a year by day, three years by week or month.
                 */
                from?: components["parameters"]["ReportFrom"];
                /** @description The period's last day (UTC), included; needs from. */
                to?: components["parameters"]["ReportTo"];
                /** @description The rows' span; a row's day is its bucket's first (a week starts on Monday). */
                bucket?: components["parameters"]["ReportBucket"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The buckets. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["UsersBucket"][];
                        partial: "bots"[];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    housePnLReport: {
        parameters: {
            query?: {
                /** @description Days back, today included. */
                days?: components["parameters"]["Days"];
                /**
                 * @description The period's first day (UTC), instead of days; to defaults to
                 *     today. At most a year by day, three years by week or month.
                 */
                from?: components["parameters"]["ReportFrom"];
                /** @description The period's last day (UTC), included; needs from. */
                to?: components["parameters"]["ReportTo"];
                /** @description The rows' span; a row's day is its bucket's first (a week starts on Monday). */
                bucket?: components["parameters"]["ReportBucket"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The buckets. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["HousePnLBucket"][];
                        unpriced: string[];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listContractStates: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The contracts. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        contracts: components["schemas"]["ContractState"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    setContractStatus: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                symbol: components["parameters"]["Contract"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["StatusRequest"];
            };
        };
        responses: {
            /** @description Halted. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StatusResult"];
                };
            };
            /** @description The change confirmed, waiting. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StatusResult"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    previewContractStatus: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                symbol: components["parameters"]["Contract"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @enum {string} */
                    to: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
                };
            };
        };
        responses: {
            /** @description The move. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["StatusPreview"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    liftReduceOnly: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                symbol: components["parameters"]["Contract"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The outcome. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        symbol: string;
                        lifted: boolean;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listRiskPositions: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The positions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        positions: components["schemas"]["RiskPosition"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listLiquidations: {
        parameters: {
            query?: {
                /** @description Days back, today included. */
                days?: components["parameters"]["Days"];
                /** @description Empty for all. */
                kind?: "WARNING" | "STARTED" | "FILLED" | "ADL";
                /** @description One contract (BTC-USDT-PERP). */
                symbol?: string;
                user_id?: components["parameters"]["UserFilter"];
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of steps. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["LiquidationStep"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listPositions: {
        parameters: {
            query?: {
                symbol?: string;
                user_id?: components["parameters"]["UserFilter"];
                /** @description true for the positions under watch only (warned, their cross account warned, taken over, margin ratio ≥ 0.5); never HOUSE's. */
                watch?: "true";
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The positions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        positions: components["schemas"]["RiskPosition"][];
                        truncated: boolean;
                        /** @description HOUSE's account on the contracts (its positions are the counterparty of users'). */
                        house_user_id: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getInsuranceFund: {
        parameters: {
            query?: {
                asset?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The balances. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["InsuranceFund"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    requestInsuranceFunding: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description Every request that moves money carries one (C5.5 ⑥): the console makes a key per operation and sends it again with
                 *     each retry. The first request with a key makes the operation (its approval, hold, closing order or message takes
                 *     its ID from the key); the same request again answers with what it made, finishing a fund operation whose outcome
                 *     was unknown; the key with another request fails with 409 COMMON_IDEMPOTENCY_CONFLICT. Keys are each
                 *     administrator's own and kept 24 hours. Missing or longer than 128 characters: 400.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @default USDT */
                    asset?: string;
                    /** @description Positive. */
                    amount: components["schemas"]["Decimal"];
                    reason: string;
                    reference?: string;
                    /** @default false */
                    direct?: boolean;
                };
            };
        };
        responses: {
            /** @description The request. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Approval"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getHouse: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The book. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["House"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getHealth: {
        parameters: {
            query?: {
                details?: "true";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The services. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        services: components["schemas"]["ServiceHealth"][];
                        /** @description With details; left out when market-data-service could not be asked. */
                        feed?: components["schemas"]["FeedStatus"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getSystemBalances: {
        parameters: {
            query?: {
                /** @description One asset; every asset when empty. */
                asset?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The accounts by type and asset. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        balances: {
                            account_type: string;
                            asset: string;
                            available: components["schemas"]["Decimal"];
                            frozen: components["schemas"]["Decimal"];
                        }[];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getReconciliation: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The runs. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        latest: components["schemas"]["ReconciliationRun"][];
                        failures: components["schemas"]["ReconciliationRun"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getCustody: {
        parameters: {
            query?: {
                /**
                 * @description The custodian: UDUN (the default) or UDUNMOCK, the stand-in that
                 *     serves only the hidden test asset (ADR-0017).
                 */
                provider?: "UDUN" | "UDUNMOCK";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The custody wallet. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustodyOverview"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listCustodyCallbacks: {
        parameters: {
            query?: {
                /**
                 * @description The custodian: UDUN (the default) or UDUNMOCK, the stand-in that
                 *     serves only the hidden test asset (ADR-0017).
                 */
                provider?: "UDUN" | "UDUNMOCK";
                /** @description One outcome; every outcome when empty. */
                result?: "RECEIVED" | "APPLIED" | "IGNORED" | "UNMATCHED" | "REJECTED" | "FAILED" | "DISCREPANCY";
                kind?: "DEPOSIT" | "WITHDRAWAL";
                /** @description A trade ID, withdrawal ID (businessId), transaction hash or address. */
                q?: string;
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of callbacks. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["CustodyCallback"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getCustodyCallback: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["CallbackID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The callback. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustodyCallback"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    replayCustodyCallback: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["CallbackID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The callback after the replay. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustodyCallback"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listCustodyFees: {
        parameters: {
            query?: {
                /** @description The custodian (UDUN, UDUNMOCK); every custodian's when empty. */
                provider?: "UDUN" | "UDUNMOCK";
                /** @description One status; every status when empty. */
                status?: "HELD" | "BOOKABLE" | "WRITTEN_OFF";
                /** @description The previous page's next_cursor; omitted for the first page. */
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description A page of fees. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["CustodyFee"][];
                        next_cursor: components["schemas"]["NextCursor"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    bookCustodyFee: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description The withdrawal the custodian charged the fee on. */
                withdrawal_id: components["parameters"]["FeeWithdrawalID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @description The asset it was charged in; the reported one when absent. */
                    asset?: string;
                    amount?: components["schemas"]["Decimal"];
                    reason: string;
                };
            };
        };
        responses: {
            /** @description The fee as decided. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustodyFee"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    writeOffCustodyFee: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description The withdrawal the custodian charged the fee on. */
                withdrawal_id: components["parameters"]["FeeWithdrawalID"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["Reason"];
            };
        };
        responses: {
            /** @description The fee as decided. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["CustodyFee"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
