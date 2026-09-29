// Generated from api/admin/admin.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
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
         *     beyond 10 attempts a minute from one IP.
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
         * Withdrawals in a status, oldest first
         * @description Needs withdrawals.read.
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
         * Assets (with networks) and trading pairs
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
         * Two-person requests, newest first (at most 100)
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
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
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
            permissions: ("users.read" | "users.status" | "orders.cancel" | "instruments.read" | "instruments.write" | "flags.read" | "flags.write" | "withdrawals.read" | "withdrawals.review" | "ledger.adjust.request" | "ledger.adjust.approve" | "audit.read")[];
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
            kind: "LEDGER_ADJUSTMENT";
            /** @description For LEDGER_ADJUSTMENT user_id, asset and amount. */
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
        UserID: string;
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
                    totp_code: string;
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
                /** @description A withdrawal status (appendix B), e.g. APPROVED, BROADCAST, CONFIRMED, REJECTED, FAILED. */
                status?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The withdrawals. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Withdrawal"][];
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
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The requests. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Approval"][];
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
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The entries. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["AuditEntry"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
