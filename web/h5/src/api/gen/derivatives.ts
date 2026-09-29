// Generated from api/openapi/derivatives.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/derivatives/account": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's FUTURES account at the mark prices */
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
         *     the risk limit of the new leverage (DERIV_RISK_LIMIT_EXCEEDED); a
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
         *     position left to close), DERIV_RISK_LIMIT_EXCEEDED,
         *     DERIV_INSUFFICIENT_MARGIN (the available balance less the cross
         *     positions' unrealized loss), ORDER_TOO_MANY_OPEN or the USER_
         *     eligibility codes. A client_order_id repeated with the same order
         *     returns that order.
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
}
export type webhooks = Record<string, never>;
export interface components {
    schemas: {
        FuturesAccount: {
            /** @example USDT */
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
            /** @description The estimate of an isolated position; null for cross positions. */
            liquidation_price: components["schemas"]["NullableDecimal"];
            realized_pnl: components["schemas"]["Decimal"];
            /** @description Funding received (positive) or paid. */
            funding: components["schemas"]["Decimal"];
            /** Format: date-time */
            updated_at: string;
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
            cancel_reason: string | null;
            reject_reason: string | null;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
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
            /** @description In the settlement asset (USDT). */
            fee: components["schemas"]["Decimal"];
            realized_pnl: components["schemas"]["Decimal"];
            liquidation: boolean;
            /** @description False while the ledger has not booked it (see the runbook). */
            settled: boolean;
            /** Format: date-time */
            executed_at: string;
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
            query?: never;
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
}
