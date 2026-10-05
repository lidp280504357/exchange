// Generated from api/openapi/trading.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/orders": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's orders, newest first */
        get: operations["listOrders"];
        put?: never;
        /**
         * Place a spot order
         * @description Limit orders need price and quantity; market buys spend
         *     quote_amount, market sells sell quantity. A buy freezes quote
         *     (price x quantity rounded up to the quote asset, or quote_amount),
         *     a sell freezes base. Checks fail with INSTRUMENT_NOT_TRADING,
         *     INSTRUMENT_PRECISION, ORDER_QUANTITY_OUT_OF_RANGE,
         *     ORDER_MIN_NOTIONAL, ORDER_PRICE_OUT_OF_BAND, ORDER_TOO_MANY_OPEN or
         *     the USER_ eligibility codes, and nothing is stored. When the ledger
         *     refuses the freeze (LEDGER_INSUFFICIENT_BALANCE) the order is stored
         *     as REJECTED and the error's details carry order_id. A
         *     client_order_id repeated with the same order returns that order; with
         *     another order it fails with COMMON_IDEMPOTENCY_CONFLICT.
         *
         *     Margin (design 2026-10-06, batch E2): account MARGIN_CROSS or
         *     MARGIN_ISOLATED trades from that margin account (the isolated one of
         *     the order's pair). The funds checked are the free balance plus, with
         *     side_effect AUTO_BORROW, what the account may borrow (the difference
         *     is borrowed before the freeze); AUTO_REPAY repays the asset bought
         *     or received with what the fills bring. The order may not leave the
         *     margin level under the warning level (MARGIN_LEVEL_TOO_LOW); further
         *     checks fail with MARGIN_DISABLED, MARGIN_FROZEN, MARGIN_LIMIT,
         *     MARGIN_POOL_EMPTY or MARGIN_ASSET_NOT_BORROWABLE (margin contract).
         *     Until batch E2 only account SPOT and side_effect NONE are accepted.
         */
        post: operations["createOrder"];
        /**
         * Cancel the caller's active orders
         * @description Asks the engine to cancel every active order, of one pair when symbol is given; the orders change as the engine answers.
         */
        delete: operations["cancelOrders"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orders/{order_id}": {
        parameters: {
            query?: never;
            header?: never;
            path: {
                order_id: string;
            };
            cookie?: never;
        };
        /** One of the caller's orders */
        get: operations["getOrder"];
        put?: never;
        post?: never;
        /**
         * Cancel one order
         * @description Asks the engine to cancel the order; it becomes CANCELED when the
         *     engine confirms (the frozen rest is then released). A filled order
         *     fails with ORDER_ALREADY_FILLED, another finished one with
         *     COMMON_CONFLICT. Canceling again is harmless.
         */
        delete: operations["cancelOrder"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/orders/{order_id}/fills": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The fills of one of the caller's orders, oldest first */
        get: operations["listOrderFills"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/fills": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's fills, newest first */
        get: operations["listFills"];
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
        Order: {
            /** Format: uuid */
            order_id: string;
            client_order_id: string;
            symbol: string;
            /** @enum {string} */
            side: "BUY" | "SELL";
            /** @enum {string} */
            type: "LIMIT" | "MARKET";
            /** @enum {string} */
            time_in_force: "GTC" | "IOC" | "FOK" | "POST_ONLY";
            /** @enum {string} */
            self_trade_prevention: "CANCEL_NEWEST" | "CANCEL_OLDEST" | "CANCEL_BOTH";
            price?: components["schemas"]["Decimal"];
            quantity?: components["schemas"]["Decimal"];
            quote_amount?: components["schemas"]["Decimal"];
            /** @enum {string} */
            status: "NEW" | "OPEN" | "PARTIALLY_FILLED" | "FILLED" | "CANCELED" | "REJECTED" | "EXPIRED";
            /** @description Appendix C code of a REJECTED order (LEDGER_INSUFFICIENT_BALANCE, ORDER_WOULD_TAKE, ORDER_NO_LIQUIDITY, ORDER_SELF_TRADE). */
            reject_reason?: string;
            /**
             * @description Why a CANCELED order's rest was canceled.
             * @enum {string}
             */
            cancel_reason?: "USER" | "IOC" | "FOK" | "SELF_TRADE" | "NO_LIQUIDITY";
            filled_quantity: components["schemas"]["Decimal"];
            filled_quote: components["schemas"]["Decimal"];
            frozen_asset: string;
            frozen_amount: components["schemas"]["Decimal"];
            cancel_requested: boolean;
            /**
             * @description The account the order trades from (from batch E2 of the margin design; SPOT when absent).
             * @enum {string}
             */
            account?: "SPOT" | "MARGIN_CROSS" | "MARGIN_ISOLATED";
            /** @enum {string} */
            side_effect?: "NONE" | "AUTO_BORROW" | "AUTO_REPAY";
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            updated_at: string;
        };
        Fill: {
            /** Format: uuid */
            trade_id: string;
            /** Format: uuid */
            order_id: string;
            symbol: string;
            /** @enum {string} */
            side: "BUY" | "SELL";
            /** @enum {string} */
            role: "MAKER" | "TAKER";
            price: components["schemas"]["Decimal"];
            quantity: components["schemas"]["Decimal"];
            quote_quantity: components["schemas"]["Decimal"];
            /** @description A buyer pays in the base asset, a seller in the quote asset. */
            fee_asset: string;
            fee: components["schemas"]["Decimal"];
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
    listOrders: {
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
                        items: components["schemas"]["Order"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    createOrder: {
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
                    /** @example BTC-USDT */
                    symbol: string;
                    /** @enum {string} */
                    side: "BUY" | "SELL";
                    /** @enum {string} */
                    type: "LIMIT" | "MARKET";
                    /**
                     * @description Defaults to GTC for limit orders and IOC for market orders; market orders take IOC or FOK.
                     * @enum {string}
                     */
                    time_in_force?: "GTC" | "IOC" | "FOK" | "POST_ONLY";
                    price?: components["schemas"]["Decimal"];
                    quantity?: components["schemas"]["Decimal"];
                    quote_amount?: components["schemas"]["Decimal"];
                    client_order_id?: string;
                    /**
                     * @description Defaults to CANCEL_NEWEST.
                     * @enum {string}
                     */
                    self_trade_prevention?: "CANCEL_NEWEST" | "CANCEL_OLDEST" | "CANCEL_BOTH";
                    /**
                     * @description The account the order trades from; defaults to SPOT.
                     * @enum {string}
                     */
                    account?: "SPOT" | "MARGIN_CROSS" | "MARGIN_ISOLATED";
                    /**
                     * @description Margin accounts only; defaults to NONE.
                     * @enum {string}
                     */
                    side_effect?: "NONE" | "AUTO_BORROW" | "AUTO_REPAY";
                };
            };
        };
        responses: {
            /** @description Accepted; the order is funded and on its way to the engine. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Order"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    cancelOrders: {
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
                        /** @description How many orders were asked to cancel. */
                        requested: number;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getOrder: {
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
                    "application/json": components["schemas"]["Order"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    cancelOrder: {
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
                    "application/json": components["schemas"]["Order"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listOrderFills: {
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
            /** @description The order's fills. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        fills: components["schemas"]["Fill"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listFills: {
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
                        items: components["schemas"]["Fill"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
