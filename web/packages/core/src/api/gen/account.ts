// Generated from api/openapi/account.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/account/balances": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's balances */
        get: operations["listBalances"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/account/transfers": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's transfers, newest first */
        get: operations["listTransfers"];
        put?: never;
        /**
         * Move funds between the SPOT and FUTURES accounts
         * @description Settles at once in one ledger transaction. Needs the account.transfer
         *     feature and an eligible account (USER_NOT_ELIGIBLE, USER_FROZEN,
         *     USER_RISK_REVIEW). Retrying with the same Idempotency-Key returns
         *     the same transfer, or the same error for a transfer that failed
         *     (LEDGER_INSUFFICIENT_BALANCE, whose details carry transfer_id); the
         *     same key with another body fails with COMMON_IDEMPOTENCY_CONFLICT.
         *     While an operator has the product line of the FUTURES account
         *     closed (design 2026-10-07, product switches: USDT's FUTURES
         *     account is the USDT-margined contracts', product.usdt_m; BTC's,
         *     ETH's and ASTRA's the coin-margined ones', product.coin_m) a
         *     transfer into it fails with PRODUCT_CLOSED (403, details product)
         *     and nothing is stored; transfers out stay open.
         */
        post: operations["createTransfer"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/account/ledger": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's fund flow, newest first */
        get: operations["listLedgerEntries"];
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
        Balance: {
            /** @enum {string} */
            account_type: "SPOT" | "FUTURES";
            asset: string;
            available: components["schemas"]["Decimal"];
            frozen: components["schemas"]["Decimal"];
            total: components["schemas"]["Decimal"];
        };
        Transfer: {
            /** Format: uuid */
            transfer_id: string;
            asset: string;
            amount: components["schemas"]["Decimal"];
            from_account_type: string;
            to_account_type: string;
            /** @enum {string} */
            status: "COMPLETED" | "FAILED";
            failure_reason?: string;
            /** Format: date-time */
            created_at: string;
        };
        LedgerEntry: {
            id: string;
            /** Format: uuid */
            journal_id: string;
            entry_type: string;
            account_type: string;
            asset: string;
            amount: components["schemas"]["Decimal"];
            /** @enum {string} */
            balance_kind: "AVAILABLE" | "FROZEN";
            available_after: components["schemas"]["Decimal"];
            frozen_after: components["schemas"]["Decimal"];
            /** Format: date-time */
            posted_at: string;
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
    listBalances: {
        parameters: {
            query?: {
                account_type?: "SPOT" | "FUTURES";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description One entry per account that has ever held funds. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        balances: components["schemas"]["Balance"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listTransfers: {
        parameters: {
            query?: {
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
                        items: components["schemas"]["Transfer"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    createTransfer: {
        parameters: {
            query?: never;
            header: {
                "Idempotency-Key": string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    asset: string;
                    amount: components["schemas"]["Decimal"];
                    /** @enum {string} */
                    from_account_type: "SPOT" | "FUTURES";
                    /** @enum {string} */
                    to_account_type: "SPOT" | "FUTURES";
                };
            };
        };
        responses: {
            /** @description Completed transfer. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Transfer"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listLedgerEntries: {
        parameters: {
            query?: {
                asset?: string;
                /** @description Entry type, e.g. ACCOUNT_TRANSFER. */
                type?: string;
                cursor?: components["parameters"]["Cursor"];
                limit?: components["parameters"]["Limit"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description One page of ledger lines. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["LedgerEntry"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
