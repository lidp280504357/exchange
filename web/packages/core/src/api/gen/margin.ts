// Generated from api/openapi/margin.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/margin/assets": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * What can be borrowed and used as margin, at what rate
         * @description Public. Every asset of the margin list with its terms, its current hourly rate and what the pool has left.
         */
        get: operations["listMarginAssets"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/pairs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The cross account's terms and each pair's isolated terms
         * @description Public. Leverage, warning and liquidation levels.
         */
        get: operations["listMarginPairs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/accounts": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The caller's cross account and isolated accounts
         * @description The cross account always (empty until the first transfer) and every
         *     isolated account that ever held something, with their assets,
         *     debts, margin level and values in USDT.
         */
        get: operations["listMarginAccounts"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/transfer": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Move an asset between the spot account and a margin account
         * @description IN moves from SPOT into the margin account (an isolated account is
         *     opened by its first transfer and takes only its pair's two assets;
         *     MARGIN_DISABLED while the switch is off). OUT moves back what is free
         *     of open orders and of the asset's own debt
         *     (LEDGER_INSUFFICIENT_BALANCE, details max_transferable), and while
         *     the account owes anything only what keeps the margin level at or
         *     above the warning level (MARGIN_LEVEL_TOO_LOW, details
         *     max_transferable; MARGIN_PRICE_UNAVAILABLE without prices). A frozen
         *     or liquidating account moves nothing (MARGIN_FROZEN). While spot
         *     trading is closed (product.spot; GET /v1/platform/products) IN takes
         *     only an asset the account owes (principal or interest), whatever
         *     the amount, so a debt stays payable; any other fails with
         *     PRODUCT_CLOSED (403, details product: spot). OUT goes on.
         */
        post: operations["marginTransfer"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/borrow": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Borrow an asset into a margin account
         * @description The amount must be borrowable (MARGIN_ASSET_NOT_BORROWABLE), within
         *     what the account may borrow at its leverage and the user's cap
         *     (MARGIN_LIMIT), what the pool has left (MARGIN_POOL_EMPTY), and leave
         *     the margin level at or above the warning level
         *     (MARGIN_LEVEL_TOO_LOW); the account must have been opened by a
         *     transfer in and not be frozen (MARGIN_FROZEN), and every asset of it
         *     priced (MARGIN_PRICE_UNAVAILABLE). MARGIN_DISABLED while the switch
         *     is off; PRODUCT_CLOSED (403, details product: spot) while spot
         *     trading is closed (product.spot; repaying and transfers out go on).
         *     The first hour's interest is charged at once.
         */
        post: operations["marginBorrow"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/repay": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Repay a debt from the margin account's free balance
         * @description Interest first, then principal. ALL repays the whole debt of the
         *     asset, as far as the free balance goes. Open while the switch is off
         *     and while an operator froze the account; a liquidation in progress
         *     refuses it (MARGIN_FROZEN). More than the debt fails with
         *     MARGIN_REPAY_EXCEEDS_DEBT, more than the free balance with
         *     LEDGER_INSUFFICIENT_BALANCE; a repayment that meets another of the
         *     account's still on its way may fail with LEDGER_INTEREST_FIRST or
         *     LEDGER_DEBT_OVERPAID (retry with a new key).
         */
        post: operations["marginRepay"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/loans": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The caller's open loans
         * @description One per account and asset with principal or interest outstanding.
         */
        get: operations["listMarginLoans"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/interest": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's interest charges, newest first */
        get: operations["listMarginInterest"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/liquidations": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's liquidations, newest first */
        get: operations["listMarginLiquidations"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/margin/max-borrowable": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * How much of an asset the account may borrow now
         * @description min(net assets x (leverage - 1) - what is borrowed, what keeps the
         *     margin level after the borrow at or above the warning level, what
         *     the pool has left, the user's cap), in the asset at its current
         *     value.
         */
        get: operations["getMaxBorrowable"];
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
        /** @enum {string} */
        MarginAccountType: "MARGIN_CROSS" | "MARGIN_ISOLATED";
        MarginAsset: {
            asset: string;
            borrowable: boolean;
            /** @description Counts in the total assets of a margin account. */
            collateral: boolean;
            /** @description What the asset counts for as margin, e.g. 0.95 (USDT 1). */
            haircut: components["schemas"]["Decimal"];
            /** @description What the platform lends of the asset at most. */
            pool_cap: components["schemas"]["Decimal"];
            /** @description pool_cap less what is lent out. */
            pool_available: components["schemas"]["Decimal"];
            /** @description What is lent out over pool_cap, from 0 to 1; the FLOATING model's rate follows it. */
            utilization: components["schemas"]["Decimal"];
            /** @description What one user may owe of the asset at most. */
            user_cap: components["schemas"]["Decimal"];
            /** @enum {string} */
            interest_model: "FIXED" | "FLOATING";
            /** @description This hour's rate, e.g. 0.00001 for 0.0010% an hour. */
            hourly_rate: components["schemas"]["Decimal"];
            /**
             * Format: date-time
             * @description The hour hourly_rate applies to (FLOATING sets it on the hour from the pool's use).
             */
            rate_hour: string;
            floating: components["schemas"]["FloatingRate"];
        };
        /**
         * @description The FLOATING model's hourly rate by the pool's utilization u: from
         *     base_rate at u = 0 linearly to kink_rate at u = kink, then linearly
         *     to max_rate at u = 1 (design §4.1).
         */
        FloatingRate: {
            base_rate: components["schemas"]["Decimal"];
            kink: components["schemas"]["Decimal"];
            kink_rate: components["schemas"]["Decimal"];
            max_rate: components["schemas"]["Decimal"];
        };
        MarginTerms: {
            leverage: number;
            /** @description Margin level under which the user is warned, e.g. 1.3. */
            warn_level: components["schemas"]["Decimal"];
            /** @description Margin level at which the account is liquidated, e.g. 1.1. */
            liquidation_level: components["schemas"]["Decimal"];
            /** @description Of the liquidation's traded value, e.g. 0.02. */
            liquidation_fee_rate: components["schemas"]["Decimal"];
        };
        MarginPair: components["schemas"]["MarginTerms"] & {
            symbol: string;
            base: string;
            quote: string;
            /** @description Whether the pair takes isolated accounts. */
            isolated: boolean;
        };
        MarginBalance: {
            asset: string;
            free: components["schemas"]["Decimal"];
            /** @description Frozen by open orders. */
            locked: components["schemas"]["Decimal"];
            /** @description Principal owed. */
            borrowed: components["schemas"]["Decimal"];
            /** @description Interest owed. */
            interest: components["schemas"]["Decimal"];
            /** @description free + locked - borrowed - interest. */
            net: components["schemas"]["Decimal"];
        };
        MarginAccount: {
            account: components["schemas"]["MarginAccountType"];
            /** @description The pair of an isolated account; null for the cross account. */
            symbol: string | null;
            leverage: number;
            /**
             * @description WARNED under the warning level; LIQUIDATING from the trigger to the end; FROZEN by an operator.
             * @enum {string}
             */
            status: "NORMAL" | "WARNED" | "LIQUIDATING" | "FROZEN";
            /** @description total_asset / total_liability; null without debts (shown as 999). */
            margin_level: components["schemas"]["Decimal"] | null;
            warn_level: components["schemas"]["Decimal"];
            liquidation_level: components["schemas"]["Decimal"];
            /** @description In USDT, each collateral asset after its haircut. */
            total_asset: components["schemas"]["Decimal"];
            /** @description Principal and interest in USDT. */
            total_liability: components["schemas"]["Decimal"];
            /** @description total_asset - total_liability. */
            net_asset: components["schemas"]["Decimal"];
            /** @description An isolated account's estimate of the base price at which it is liquidated; null for the cross account and without debts. */
            liquidation_price: components["schemas"]["Decimal"] | null;
            balances: components["schemas"]["MarginBalance"][];
            /** Format: date-time */
            updated_at: string;
        };
        MarginTransfer: {
            /** Format: uuid */
            transfer_id: string;
            /** @enum {string} */
            direction: "IN" | "OUT";
            account: components["schemas"]["MarginAccountType"];
            symbol: string | null;
            asset: string;
            amount: components["schemas"]["Decimal"];
            /** Format: date-time */
            created_at: string;
        };
        MarginLoan: {
            account: components["schemas"]["MarginAccountType"];
            symbol: string | null;
            asset: string;
            principal: components["schemas"]["Decimal"];
            interest: components["schemas"]["Decimal"];
            /** @enum {string} */
            interest_model: "FIXED" | "FLOATING";
            hourly_rate: components["schemas"]["Decimal"];
            /** Format: date-time */
            updated_at: string;
        };
        MarginInterest: {
            /** Format: uuid */
            interest_id: string;
            account: components["schemas"]["MarginAccountType"];
            symbol: string | null;
            asset: string;
            /** @description The principal the hour was charged on. */
            principal: components["schemas"]["Decimal"];
            hourly_rate: components["schemas"]["Decimal"];
            /** @description principal x hourly_rate, rounded up to the asset's precision. */
            interest: components["schemas"]["Decimal"];
            /** @enum {string} */
            interest_model: "FIXED" | "FLOATING";
            /**
             * Format: date-time
             * @description The hour charged (a borrow's first hour at the time of borrowing).
             */
            hour: string;
        };
        MarginLiquidation: {
            /** Format: uuid */
            liquidation_id: string;
            account: components["schemas"]["MarginAccountType"];
            symbol: string | null;
            /** @enum {string} */
            status: "STARTED" | "COMPLETED";
            /** @description The margin level that triggered it. */
            margin_level: components["schemas"]["Decimal"];
            /** @description Debts repaid, per asset (principal and interest). */
            repaid: {
                asset: string;
                amount: components["schemas"]["Decimal"];
            }[];
            /** @description The liquidation fee in USDT, to the insurance fund. */
            fee: components["schemas"]["Decimal"];
            /** @description In USDT, what the insurance fund paid of debts the assets did not cover. */
            insurance_covered: components["schemas"]["Decimal"];
            /** Format: date-time */
            started_at: string;
            /** Format: date-time */
            completed_at: string | null;
        };
        /**
         * @description A message of the private WebSocket channel "margin" (its data):
         *     ACCOUNT when an account's assets, debts or margin level change (at
         *     most once a second per account), WARNING when it falls under the
         *     warning level, LIQUIDATION when a liquidation starts and completes.
         */
        MarginPush: {
            /** @enum {string} */
            type: "ACCOUNT" | "WARNING" | "LIQUIDATION";
            account?: components["schemas"]["MarginAccount"];
            warning?: {
                account: components["schemas"]["MarginAccountType"];
                symbol: string | null;
                margin_level: components["schemas"]["Decimal"];
                warn_level: components["schemas"]["Decimal"];
                liquidation_level: components["schemas"]["Decimal"];
            };
            liquidation?: components["schemas"]["MarginLiquidation"];
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
        /** @example 0.00001 */
        Decimal: string;
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
         * @description The client's key of the write (1 to 100 bytes): the same key with
         *     the same request returns the first result, with another request
         *     COMMON_IDEMPOTENCY_CONFLICT.
         */
        IdempotencyKey: string;
        AccountQuery: components["schemas"]["MarginAccountType"];
        /** @description The pair of a MARGIN_ISOLATED account. */
        SymbolQuery: string;
        Cursor: string;
        Limit: number;
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
    listMarginAssets: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The assets. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["MarginAsset"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listMarginPairs: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The terms. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        cross: components["schemas"]["MarginTerms"];
                        items: components["schemas"]["MarginPair"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listMarginAccounts: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The accounts. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        cross: components["schemas"]["MarginAccount"];
                        isolated: components["schemas"]["MarginAccount"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    marginTransfer: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description The client's key of the write (1 to 100 bytes): the same key with
                 *     the same request returns the first result, with another request
                 *     COMMON_IDEMPOTENCY_CONFLICT.
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
                    direction: "IN" | "OUT";
                    account: components["schemas"]["MarginAccountType"];
                    /**
                     * @description The pair of a MARGIN_ISOLATED account.
                     * @example BTC-USDT
                     */
                    symbol?: string;
                    asset: string;
                    amount: components["schemas"]["Decimal"];
                };
            };
        };
        responses: {
            /** @description Done. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MarginTransfer"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    marginBorrow: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description The client's key of the write (1 to 100 bytes): the same key with
                 *     the same request returns the first result, with another request
                 *     COMMON_IDEMPOTENCY_CONFLICT.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    account: components["schemas"]["MarginAccountType"];
                    symbol?: string;
                    asset: string;
                    amount: components["schemas"]["Decimal"];
                };
            };
        };
        responses: {
            /** @description Borrowed; the loan as it stands now. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MarginLoan"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    marginRepay: {
        parameters: {
            query?: never;
            header: {
                /**
                 * @description The client's key of the write (1 to 100 bytes): the same key with
                 *     the same request returns the first result, with another request
                 *     COMMON_IDEMPOTENCY_CONFLICT.
                 */
                "Idempotency-Key": components["parameters"]["IdempotencyKey"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    account: components["schemas"]["MarginAccountType"];
                    symbol?: string;
                    asset: string;
                    amount: components["schemas"]["Decimal"] | "ALL";
                };
            };
        };
        responses: {
            /** @description Repaid. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        interest_repaid: components["schemas"]["Decimal"];
                        principal_repaid: components["schemas"]["Decimal"];
                        loan: components["schemas"]["MarginLoan"];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listMarginLoans: {
        parameters: {
            query?: {
                account?: components["parameters"]["AccountQuery"];
                /** @description The pair of a MARGIN_ISOLATED account. */
                symbol?: components["parameters"]["SymbolQuery"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The loans. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["MarginLoan"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listMarginInterest: {
        parameters: {
            query?: {
                account?: components["parameters"]["AccountQuery"];
                /** @description The pair of a MARGIN_ISOLATED account. */
                symbol?: components["parameters"]["SymbolQuery"];
                asset?: string;
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description One page. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["MarginInterest"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listMarginLiquidations: {
        parameters: {
            query?: {
                account?: components["parameters"]["AccountQuery"];
                /** @description The pair of a MARGIN_ISOLATED account. */
                symbol?: components["parameters"]["SymbolQuery"];
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description One page. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["MarginLiquidation"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getMaxBorrowable: {
        parameters: {
            query: {
                account: components["schemas"]["MarginAccountType"];
                /** @description The pair of a MARGIN_ISOLATED account. */
                symbol?: components["parameters"]["SymbolQuery"];
                asset: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The amount. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        asset: string;
                        amount: components["schemas"]["Decimal"];
                        /**
                         * @description Which of the four bounds is the smallest.
                         * @enum {string}
                         */
                        limited_by: "LEVERAGE" | "LEVEL" | "POOL" | "USER_CAP";
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
