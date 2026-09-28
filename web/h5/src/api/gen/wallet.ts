// Generated from api/openapi/wallet.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/wallet/deposit-address": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The caller's deposit address for an asset on a network
         * @description Assigns the next derived address on first use; the same address
         *     serves every asset of the network. Fails with
         *     WALLET_NETWORK_UNKNOWN for a network the asset does not have,
         *     WALLET_NETWORK_DISABLED while the asset or the network takes no
         *     deposits, WALLET_UNAVAILABLE before the deposit key is configured,
         *     and the USER_ eligibility codes.
         */
        get: operations["getDepositAddress"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/wallet/deposits": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's deposits, newest first */
        get: operations["listDeposits"];
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
        DepositAddress: {
            asset: string;
            network: string;
            /**
             * @description EIP-55 checksummed.
             * @example 0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed
             */
            address: string;
            /** @description The token contract; null for the chain's coin. */
            contract: string | null;
            min_deposit: components["schemas"]["Decimal"];
            /** @description Confirmations before the deposit is credited. */
            confirmations: number;
        };
        Deposit: {
            /** Format: uuid */
            id: string;
            /** @description Null for a token the platform does not list. */
            asset: string | null;
            network: string;
            address: string;
            contract: string | null;
            tx_hash: string;
            /** @description The token transfer's log index; -1 for the chain's coin. */
            log_index: number;
            /** Format: int64 */
            block_number: number;
            amount: components["schemas"]["Decimal"];
            /** @description The transferred integer in the chain's smallest unit. */
            raw_amount: string;
            confirmations: number;
            required_confirmations: number;
            /**
             * @description ORPHANED: a chain reorganization dropped the transaction before
             *     it was credited. REJECTED: not credited to the account (reason
             *     says why).
             * @enum {string}
             */
            status: "DETECTED" | "CONFIRMING" | "CONFIRMED" | "CREDITED" | "ORPHANED" | "REJECTED";
            /** @description Held in UNCLAIMED_DEPOSIT instead of the account. */
            unclaimed: boolean;
            /** @enum {string|null} */
            reason: "BELOW_MINIMUM" | "ACCOUNT_CLOSED" | "NOT_ELIGIBLE" | "UNSUPPORTED_TOKEN" | null;
            /** Format: date-time */
            detected_at: string;
            /** Format: date-time */
            confirmed_at: string | null;
            /** Format: date-time */
            credited_at: string | null;
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
    parameters: never;
    requestBodies: never;
    headers: {
        /** @description W3C trace ID of the request, shared by logs and events. */
        "X-Trace-Id": string;
    };
    pathItems: never;
}
export type $defs = Record<string, never>;
export interface operations {
    getDepositAddress: {
        parameters: {
            query: {
                asset: string;
                network: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The address. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["DepositAddress"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listDeposits: {
        parameters: {
            query?: {
                /** @description next_cursor of the previous page. */
                cursor?: string;
                limit?: number;
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
                        items: components["schemas"]["Deposit"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
