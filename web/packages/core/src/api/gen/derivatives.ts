// Generated from api/openapi/derivatives.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/derivatives/account": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's FUTURES account of an asset at the mark prices */
        get: operations["getDerivativesAccount"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/settings/{symbol}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A contract, e.g. BTC-USDT-PERP (case-insensitive). */
                symbol: components["parameters"]["ContractSymbol"];
            };
            cookie?: never;
        };
        /**
         * The caller's settings on a contract
         * @description One-way, cross and 20x (or the contract's most) until changed.
         */
        get: operations["getDerivativesSettings"];
        /**
         * Change the caller's settings on a contract
         * @description The position mode and the margin mode change only without positions
         *     or active orders on the contract (DERIV_SETTINGS_LOCKED). A new
         *     leverage is checked against the open positions: each must fit in
         *     the risk limit of the new leverage (DERIV_RISK_LIMIT_EXCEEDED, with
         *     details max_notional, leverage and the position's notional); a
         *     cross position's margin becomes its entry cost / leverage, frozen or
         *     freed (DERIV_INSUFFICIENT_MARGIN when more cannot be frozen); an
         *     isolated position's margin must still cover that.
         */
        put: operations["updateDerivativesSettings"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/positions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's open positions */
        get: operations["listPositions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/positions/{symbol}/margin": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A contract, e.g. BTC-USDT-PERP (case-insensitive). */
                symbol: components["parameters"]["ContractSymbol"];
            };
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Add margin to an isolated position, or take some away
         * @description A positive amount freezes more of the available balance for the
         *     position; a negative one frees margin as long as what stays covers
         *     the initial margin at the entry price, and with the unrealized
         *     result the initial margin at the mark price
         *     (DERIV_MARGIN_REDUCE_TOO_LARGE).
         */
        post: operations["adjustMargin"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/orders": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's contract orders, newest first */
        get: operations["listDerivativesOrders"];
        put?: never;
        /**
         * Place a contract order
         * @description One-way mode takes position_side BOTH (the default) and may send
         *     reduce_only; hedge mode takes LONG or SHORT, where BUY LONG and SELL
         *     SHORT open and SELL LONG and BUY SHORT close. Checks fail with
         *     INSTRUMENT_NOT_TRADING, INSTRUMENT_PRECISION,
         *     ORDER_QUANTITY_OUT_OF_RANGE, ORDER_MIN_NOTIONAL,
         *     ORDER_PRICE_OUT_OF_BAND (a limit price further from the mark than
         *     the band), DERIV_MARK_PRICE_UNAVAILABLE, DERIV_REDUCE_ONLY_MODE (the
         *     contract is degraded), DERIV_REDUCE_ONLY_REJECTED (more than the
         *     position left to close), DERIV_RISK_LIMIT_EXCEEDED (the side's
         *     position, its opening orders and this one at the mark price above
         *     the leverage's cap; details max_notional, leverage and notional),
         *     DERIV_INSUFFICIENT_MARGIN (the available balance less the cross
         *     positions' unrealized loss), ORDER_TOO_MANY_OPEN or the USER_
         *     eligibility codes; on a coin-margined contract also
         *     DERIV_CONTRACTS_NOT_INTEGER (a fraction of a contract) and, while
         *     derivatives.coin_m is off for the caller, USER_NOT_ELIGIBLE. A
         *     client_order_id repeated with the same order returns that order.
         *
         *     Product lines (design 2026-10-07, product switches): while an
         *     operator has the contract's line closed (product.usdt_m for a
         *     USDT-margined contract, product.coin_m for a coin-margined one;
         *     GET /v1/platform/products) only reduce-only orders that close a
         *     position are taken; any other, and any new conditional order,
         *     fails with PRODUCT_CLOSED (403, details product: usdt_m or
         *     coin_m). Cancels go on; the open and conditional orders were
         *     canceled when it closed (and any that slipped in, within 5
         *     seconds); positions, funding and liquidations go on, closing
         *     against HOUSE, which keeps quoting.
         */
        post: operations["createDerivativesOrder"];
        /** Cancel the caller's active contract orders */
        delete: operations["cancelDerivativesOrders"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/orders/{order_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                order_id: string;
            };
            cookie?: never;
        };
        /** One of the caller's contract orders */
        get: operations["getDerivativesOrder"];
        put?: never;
        post?: never;
        /**
         * Cancel one contract order
         * @description A filled order fails with ORDER_ALREADY_FILLED, another finished one with COMMON_CONFLICT.
         */
        delete: operations["cancelDerivativesOrder"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/fills": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's contract fills, newest first */
        get: operations["listDerivativesFills"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/conditional-orders": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's take-profit and stop-loss orders, newest first */
        get: operations["listConditionalOrders"];
        put?: never;
        /**
         * Place a take-profit or stop-loss on an open position
         * @description Waits until the trigger price is reached by the mark price
         *     (trigger_by MARK, the default) or the last trade price (LAST), then
         *     places an order that only closes the position: a market order
         *     (protected IOC, the default) or a limit order at price, for
         *     quantity or, without it, the whole position left. A long's
         *     take-profit triggers at or above the trigger, its stop-loss at or
         *     below; a short's the other way round. A trigger the price already
         *     reached fails with DERIV_TRIGGER_IMMEDIATE; without a position,
         *     DERIV_NO_POSITION. At most 20 active per contract. While the
         *     contract's product line is closed (design 2026-10-07, product
         *     switches) a new one fails with PRODUCT_CLOSED (403, details
         *     product: usdt_m or coin_m).
         */
        post: operations["createConditionalOrder"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/conditional-orders/{conditional_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Cancel an active take-profit or stop-loss */
        delete: operations["cancelConditionalOrder"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/derivatives/funding": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The caller's funding payments, newest first
         * @description At each funding time (00:00, 08:00, 16:00 UTC for an 8-hour
         *     interval) every position held pays or receives |quantity| x mark
         *     price x |rate| at the period's settled rate and mark price
         *     (GET /v1/market/{symbol}/funding-rates): longs pay shorts when the
         *     rate is positive. A payer rounds up, a receiver down. An isolated
         *     position still open pays out of its margin and receives into it;
         *     otherwise the available balance.
         */
        get: operations["listFundingPayments"];
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
        FuturesAccount: {
            /**
             * @description The settlement asset the account and its amounts are in.
             * @example USDT
             * @example BTC
             */
            asset: string;
            /** @description available + frozen. */
            wallet_balance: components["schemas"]["Decimal"];
            available: components["schemas"]["Decimal"];
            /** @description order_margin + position_margin. */
            frozen: components["schemas"]["Decimal"];
            /** @description What open orders reserve (initial margin and taker fee). */
            order_margin: components["schemas"]["Decimal"];
            position_margin: components["schemas"]["Decimal"];
            unrealized_pnl: components["schemas"]["Decimal"];
            cross_unrealized_pnl: components["schemas"]["Decimal"];
            /** @description wallet_balance + unrealized_pnl. */
            margin_balance: components["schemas"]["Decimal"];
            /** @description What may move to SPOT now, min(available, available + cross_unrealized_pnl). */
            transferable: components["schemas"]["Decimal"];
        };
        ContractSettings: {
            symbol: string;
            /** @enum {string} */
            position_mode: "ONE_WAY" | "HEDGE";
            /** @enum {string} */
            margin_mode: "CROSS" | "ISOLATED";
            leverage: number;
            /**
             * Format: date-time
             * @description Null for the defaults.
             */
            updated_at: string | null;
        };
        ContractPosition: {
            /** Format: uuid */
            position_id: string;
            symbol: string;
            /** @enum {string} */
            position_side: "BOTH" | "LONG" | "SHORT";
            /** @description Signed: positive long, negative short. */
            quantity: components["schemas"]["Decimal"];
            entry_price: components["schemas"]["Decimal"];
            /** @description Null before the contract's first mark price; so are the figures at it. */
            mark_price: components["schemas"]["NullableDecimal"];
            notional: components["schemas"]["NullableDecimal"];
            unrealized_pnl: components["schemas"]["NullableDecimal"];
            margin: components["schemas"]["Decimal"];
            /** @enum {string} */
            margin_mode: "CROSS" | "ISOLATED";
            leverage: number;
            maintenance_margin: components["schemas"]["NullableDecimal"];
            /** @description The liquidation estimate (requirements §11.7): an isolated position's own; a cross position's against the whole cross account (the available balance, the cross orders' reservations and every cross position at its mark). Null when there is none (a long the account covers down to zero) or before the first mark price. */
            liquidation_price: components["schemas"]["NullableDecimal"];
            realized_pnl: components["schemas"]["Decimal"];
            /** @description Funding received (positive) or paid. */
            funding: components["schemas"]["Decimal"];
            /** Format: date-time */
            updated_at: string;
            /** @description The asset margin, PnL and funding are in. */
            settle_asset: string;
            /** @description Signed, as quantity: whole contracts of a coin-margined contract; null for a linear one. */
            contracts: components["schemas"]["NullableDecimal"];
            /** @description A coin-margined position's value in its settlement asset at the mark price, |contracts| x contract_size / mark; null for a linear one or before the first mark price. */
            value_coin: components["schemas"]["NullableDecimal"];
            /** @description The position's value in USD: |contracts| x contract_size for a coin-margined one, notional for a linear one (null before the first mark price). */
            value_usd: components["schemas"]["NullableDecimal"];
        };
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
            /** @enum {string} */
            time_in_force: "GTC" | "IOC" | "FOK" | "POST_ONLY";
            /** @description The limit price, or a market order's protection price. */
            price: components["schemas"]["Decimal"];
            quantity: components["schemas"]["Decimal"];
            reduce_only: boolean;
            leverage: number;
            /** @enum {string} */
            margin_mode: "CROSS" | "ISOLATED";
            /** @enum {string} */
            status: "NEW" | "OPEN" | "PARTIALLY_FILLED" | "FILLED" | "CANCELED" | "REJECTED" | "EXPIRED";
            filled_quantity: components["schemas"]["Decimal"];
            average_price: components["schemas"]["NullableDecimal"];
            fee: components["schemas"]["Decimal"];
            realized_pnl: components["schemas"]["Decimal"];
            /** @description Margin and fee the order still has frozen. */
            reserved: components["schemas"]["Decimal"];
            /** @description A cancel was asked for and the engine has not confirmed it yet. */
            cancel_requested: boolean;
            cancel_reason: string | null;
            reject_reason: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
            /** @description The asset the order's margin and fee are in. */
            settle_asset: string;
        };
        ConditionalOrder: {
            /** Format: uuid */
            conditional_id: string;
            symbol: string;
            /** @enum {string} */
            position_side: "BOTH" | "LONG" | "SHORT";
            /**
             * @description The side of the order it places.
             * @enum {string}
             */
            side: "BUY" | "SELL";
            /** @enum {string} */
            kind: "TAKE_PROFIT" | "STOP_LOSS";
            trigger_price: components["schemas"]["Decimal"];
            /** @enum {string} */
            trigger_by: "MARK" | "LAST";
            /** @enum {string} */
            order_type: "MARKET" | "LIMIT";
            price: components["schemas"]["NullableDecimal"];
            /** @description Null closes the whole position. */
            quantity: components["schemas"]["NullableDecimal"];
            /** @enum {string} */
            status: "ACTIVE" | "TRIGGERED" | "CANCELED" | "FAILED";
            /** @description USER or NO_POSITION when canceled, the refusal's code when FAILED. */
            reason: string | null;
            /** @description The order it placed when TRIGGERED. */
            order_id: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
        };
        FundingPayment: {
            symbol: string;
            /** Format: date-time */
            funding_time: string;
            /** @enum {string} */
            position_side: "BOTH" | "LONG" | "SHORT";
            /** @description The signed quantity held at the funding time. */
            quantity: components["schemas"]["Decimal"];
            funding_rate: components["schemas"]["Decimal"];
            mark_price: components["schemas"]["Decimal"];
            /** @description Received (positive) or paid. */
            amount: components["schemas"]["Decimal"];
            /** @description The asset amount is in. */
            settle_asset: string;
        };
        ContractFill: {
            /** Format: uuid */
            trade_id: string;
            /** Format: uuid */
            order_id: string;
            symbol: string;
            /** @enum {string} */
            side: "BUY" | "SELL";
            /** @enum {string} */
            position_side: "BOTH" | "LONG" | "SHORT";
            /** @enum {string} */
            role: "MAKER" | "TAKER";
            price: components["schemas"]["Decimal"];
            quantity: components["schemas"]["Decimal"];
            /** @description Of the quantity, what closed a position. */
            closed_quantity: components["schemas"]["Decimal"];
            /** @description In the settlement asset (settle_asset; USDT for a linear contract). */
            fee: components["schemas"]["Decimal"];
            realized_pnl: components["schemas"]["Decimal"];
            liquidation: boolean;
            /** @description False while the ledger has not booked it (see the runbook). */
            settled: boolean;
            /** Format: date-time */
            executed_at: string;
            /** @description The asset fee and realized_pnl are in. */
            settle_asset: string;
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
        NullableDecimal: string | null;
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
        /** @description A contract, e.g. BTC-USDT-PERP (case-insensitive). */
        ContractSymbol: string;
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
    getDerivativesAccount: {
        parameters: {
            query?: {
                /** @description The settlement asset whose FUTURES account to show (USDT by default; BTC, ETH, ASTRA for the coin-margined contracts); one no contract settles in is DERIV_SETTLE_ASSET_MISMATCH. */
                asset?: string;
            };
            header?: never;
            path?: never;
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
                    "application/json": components["schemas"]["FuturesAccount"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getDerivativesSettings: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A contract, e.g. BTC-USDT-PERP (case-insensitive). */
                symbol: components["parameters"]["ContractSymbol"];
            };
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
                    "application/json": components["schemas"]["ContractSettings"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    updateDerivativesSettings: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A contract, e.g. BTC-USDT-PERP (case-insensitive). */
                symbol: components["parameters"]["ContractSymbol"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @enum {string} */
                    position_mode?: "ONE_WAY" | "HEDGE";
                    /** @enum {string} */
                    margin_mode?: "CROSS" | "ISOLATED";
                    leverage?: number;
                };
            };
        };
        responses: {
            /** @description The settings now. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContractSettings"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listPositions: {
        parameters: {
            query?: {
                symbol?: string;
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
                        positions: components["schemas"]["ContractPosition"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    adjustMargin: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A contract, e.g. BTC-USDT-PERP (case-insensitive). */
                symbol: components["parameters"]["ContractSymbol"];
            };
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /**
                     * @description BOTH (the default) in one-way mode.
                     * @enum {string}
                     */
                    position_side?: "BOTH" | "LONG" | "SHORT";
                    amount: components["schemas"]["Decimal"];
                };
            };
        };
        responses: {
            /** @description The position now. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ContractPosition"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listDerivativesOrders: {
        parameters: {
            query?: {
                symbol?: string;
                /** @description One status, or ACTIVE for NEW, OPEN and PARTIALLY_FILLED. */
                status?: "ACTIVE" | "NEW" | "OPEN" | "PARTIALLY_FILLED" | "FILLED" | "CANCELED" | "REJECTED" | "EXPIRED";
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
                        items: components["schemas"]["ContractOrder"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    createDerivativesOrder: {
        parameters: {
            query?: never;
            header?: {
                "Idempotency-Key"?: string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @example BTC-USDT-PERP */
                    symbol: string;
                    /** @enum {string} */
                    side: "BUY" | "SELL";
                    /** @enum {string} */
                    position_side?: "BOTH" | "LONG" | "SHORT";
                    /** @enum {string} */
                    type: "LIMIT" | "MARKET";
                    /**
                     * @description Defaults to GTC for limit orders and IOC for market orders; market orders take IOC or FOK.
                     * @enum {string}
                     */
                    time_in_force?: "GTC" | "IOC" | "FOK" | "POST_ONLY";
                    price?: components["schemas"]["Decimal"];
                    /** @description In the base asset, or whole contracts of a coin-margined contract (DERIV_CONTRACTS_NOT_INTEGER). */
                    quantity: components["schemas"]["Decimal"];
                    reduce_only?: boolean;
                    client_order_id?: string;
                };
            };
        };
        responses: {
            /** @description Accepted; the order's margin is reserved and it is on its way to the engine. */
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
    cancelDerivativesOrders: {
        parameters: {
            query?: {
                symbol?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Cancel requests sent. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        requested: number;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getDerivativesOrder: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                order_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The order. */
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
    cancelDerivativesOrder: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                order_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
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
    listDerivativesFills: {
        parameters: {
            query?: {
                symbol?: string;
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
                        items: components["schemas"]["ContractFill"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listConditionalOrders: {
        parameters: {
            query?: {
                symbol?: string;
                status?: "ACTIVE" | "TRIGGERED" | "CANCELED" | "FAILED";
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
                        items: components["schemas"]["ConditionalOrder"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    createConditionalOrder: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    symbol: string;
                    /**
                     * @description BOTH (the default) in one-way mode.
                     * @enum {string}
                     */
                    position_side?: "BOTH" | "LONG" | "SHORT";
                    /** @enum {string} */
                    kind: "TAKE_PROFIT" | "STOP_LOSS";
                    trigger_price: components["schemas"]["Decimal"];
                    /** @enum {string} */
                    trigger_by?: "MARK" | "LAST";
                    /** @enum {string} */
                    order_type?: "MARKET" | "LIMIT";
                    price?: components["schemas"]["Decimal"];
                    quantity?: components["schemas"]["Decimal"];
                };
            };
        };
        responses: {
            /** @description Waiting for its trigger. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConditionalOrder"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    cancelConditionalOrder: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                conditional_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Canceled. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ConditionalOrder"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listFundingPayments: {
        parameters: {
            query?: {
                symbol?: string;
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
                        items: components["schemas"]["FundingPayment"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
