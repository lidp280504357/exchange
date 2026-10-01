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
         * @description From the read model trades; user_id matches either side. Needs reports.read.
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
         *     20,000 USDT needs two distinct reviewers. Rejecting releases the
         *     frozen amount. Needs withdrawals.review.
         */
        post: operations["reviewWithdrawal"];
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
         *     int64 as strings). Assets and networks change through the
         *     versioned reference-data file (exchangectl instruments apply, run
         *     by every deploy). Needs instruments.read.
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
         *     otherwise. Needs instruments.write.
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
         * Request a manual adjustment of a user's spot balance
         * @description Creates a PENDING request for another administrator with
         *     ledger.adjust.approve. A positive amount credits the user's SPOT
         *     account against the ADJUSTMENT system account, a negative one
         *     debits it. Needs ledger.adjust.request.
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
         * Two-person requests, newest first
         * @description Needs ledger.adjust.request or audit.read.
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
         * Approve (and book) or reject another administrator's request
         * @description Fails with ADMIN_SELF_APPROVAL for one's own request and
         *     ADMIN_APPROVAL_DECIDED once decided. Approving books the
         *     adjustment with the idempotency key approval:<id>: EXECUTED with
         *     the journal, or FAILED when the ledger refuses (for instance
         *     LEDGER_ADJUSTMENT_DISABLED while the flag ledger.manual_adjustment
         *     is off). When the ledger cannot be reached the request stays
         *     PENDING and can be approved again. Needs ledger.adjust.approve.
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
    "/admin/v1/reports/trading": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Trades and orders per symbol and day (UTC), newest first
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
         * Credited deposits and confirmed withdrawals per asset and day (UTC)
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
         * Each perpetual contract's trading, funding and liquidations per day (UTC)
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
         *     TRADING or HALT → CANCEL_ONLY → DELISTED); instrument-service
         *     records the change. Needs instruments.write.
         */
        post: operations["setContractStatus"];
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
         * Request a contribution of simulated funds to the insurance fund
         * @description Creates a PENDING INSURANCE_FUND request for another administrator
         *     with ledger.adjust.approve; approving books it (ledger
         *     FundInsurance, INSURANCE_CONTRIBUTION from ADJUSTMENT), which needs
         *     the flag ledger.manual_adjustment. Needs ledger.adjust.request.
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
         * @description Asks each service's ops endpoint (/readyz) within 2 seconds. Needs reports.read.
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
            kyc_level: number;
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
            feed: null | {
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
        Reason: {
            reason: string;
        };
        Transition: {
            from: string;
            to: string;
        };
        Admin: {
            /** Format: uuid */
            id: string;
            email: string;
            name: string;
            /** @enum {string} */
            role: "ADMIN" | "OPERATOR" | "FINANCE" | "AUDITOR";
            permissions: ("users.read" | "users.status" | "orders.cancel" | "instruments.read" | "instruments.write" | "flags.read" | "flags.write" | "withdrawals.read" | "withdrawals.review" | "ledger.adjust.request" | "ledger.adjust.approve" | "audit.read" | "reports.read" | "derivatives.read" | "derivatives.write")[];
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
            status: string;
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
        Approval: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            kind: "LEDGER_ADJUSTMENT" | "INSURANCE_FUND";
            /** @description For LEDGER_ADJUSTMENT user_id, asset and amount; for INSURANCE_FUND asset and amount. */
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
            decided_by: string | null;
            /** @description The journal once executed, the ledger's refusal, or the rejection reason. */
            result: string;
            /** Format: date-time */
            created_at: string;
            decided_at: string | null;
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
            /** Format: date-time */
            warned_at: string | null;
            /** @description Maintenance margin / (margin + unrealized result); null without a mark price or once the margin is gone. */
            margin_ratio: components["schemas"]["NullableDecimal"];
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
        /** @description The previous page's next_cursor; omitted for the first page. */
        Cursor: string;
        Limit: number;
        /** @description From this time on (RFC 3339). */
        From: string;
        /** @description Before this time (RFC 3339). */
        To: string;
        UserFilter: string;
        /** @description Days back, today included. */
        Days: number;
        UserID: string;
        Contract: string;
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
    listWithdrawals: {
        parameters: {
            query?: {
                /** @description A withdrawal status (appendix B), e.g. APPROVED, BROADCAST, CONFIRMED, REJECTED, FAILED; ALL for every status. */
                status?: string;
                user_id?: components["parameters"]["UserFilter"];
                asset?: string;
                order?: "asc" | "desc";
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
    setPairStatus: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                symbol: string;
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @enum {string} */
                    to: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
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
                    "application/json": components["schemas"]["Transition"];
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
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** Format: uuid */
                    user_id: string;
                    /** @example USDT */
                    asset: string;
                    amount: components["schemas"]["Decimal"];
                    reason: string;
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
    tradingReport: {
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
                "application/json": {
                    /** @enum {string} */
                    to: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
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
                    "application/json": components["schemas"]["Transition"];
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
            header?: never;
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
            query?: never;
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
}
