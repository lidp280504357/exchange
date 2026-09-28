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
    "/v1/wallet/withdraw-addresses": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's withdrawal address book */
        get: operations["listWithdrawAddresses"];
        put?: never;
        /**
         * Add an address to the book
         * @description Needs a step-up. The address can be used from usable_at on. An
         *     address already in the book is returned as it is. Fails with
         *     WALLET_INVALID_ADDRESS, WALLET_NETWORK_UNKNOWN, WALLET_OWN_ADDRESS
         *     (the caller's own deposit address) or AUTH_STEP_UP_REQUIRED.
         */
        post: operations["addWithdrawAddress"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/wallet/withdraw-addresses/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /** Remove an address from the book */
        delete: operations["deleteWithdrawAddress"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/wallet/withdrawals": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's withdrawals, newest first */
        get: operations["listWithdrawals"];
        put?: never;
        /**
         * Request a withdrawal
         * @description Needs a step-up. Fails with WALLET_NETWORK_DISABLED,
         *     WALLET_INVALID_ADDRESS, WALLET_ADDRESS_NOT_WHITELISTED,
         *     WALLET_ADDRESS_COOLDOWN (details.usable_at), WALLET_BELOW_MINIMUM,
         *     WALLET_AMOUNT_PRECISION, WALLET_LIMIT_EXCEEDED (details: the limits
         *     and what was used), WALLET_OWN_ADDRESS, AUTH_STEP_UP_REQUIRED and the
         *     USER_ eligibility codes (withdrawals need wallet.withdraw). When the
         *     ledger refuses the freeze (LEDGER_INSUFFICIENT_BALANCE) the
         *     withdrawal is stored as REJECTED and the error's details carry
         *     withdrawal_id.
         */
        post: operations["requestWithdrawal"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/wallet/withdrawals/{id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** One of the caller's withdrawals */
        get: operations["getWithdrawal"];
        put?: never;
        post?: never;
        /**
         * Cancel a withdrawal that is not signed yet
         * @description Releases the frozen funds. Fails with WALLET_WITHDRAWAL_NOT_CANCELABLE once it is being sent.
         */
        delete: operations["cancelWithdrawal"];
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
            /**
             * @description INTERNAL for another user's withdrawal to this address, completed in the ledger (tx_hash is then internal:<withdrawal id>).
             * @enum {string}
             */
            kind: "CHAIN" | "INTERNAL";
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
        WithdrawAddress: {
            /** Format: uuid */
            id: string;
            network: string;
            /** @description EIP-55 checksummed. */
            address: string;
            label: string;
            /** Format: date-time */
            created_at: string;
            /**
             * Format: date-time
             * @description The end of the cooling-off period.
             */
            usable_at: string;
        };
        Withdrawal: {
            /** Format: uuid */
            id: string;
            asset: string;
            network: string;
            address: string;
            amount: components["schemas"]["Decimal"];
            fee: components["schemas"]["Decimal"];
            /** @description The address is another user's deposit address; the withdrawal completes inside the ledger. */
            internal: boolean;
            /** @enum {string} */
            status: "REQUESTED" | "PENDING_REVIEW" | "APPROVED" | "SIGNING" | "BROADCAST" | "CONFIRMING" | "CONFIRMED" | "INTERNAL_TRANSFER" | "REJECTED" | "CANCELED" | "FAILED";
            risk_reasons: ("NEW_ACCOUNT" | "NEW_DEVICE" | "SECURITY_CHANGE" | "NEW_ADDRESS" | "LARGE_AMOUNT" | "DAILY_SHARE")[];
            approvals_required: number;
            reject_reason: string | null;
            tx_hash: string | null;
            confirmations: number;
            required_confirmations: number;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            approved_at: string | null;
            /** Format: date-time */
            broadcast_at: string | null;
            /** Format: date-time */
            confirmed_at: string | null;
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
        /** @description A step-up token from POST /v1/auth/step-up; each works once. */
        StepUp: string;
        WithdrawalID: string;
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
    listWithdrawAddresses: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The entries, newest first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["WithdrawAddress"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    addWithdrawAddress: {
        parameters: {
            query?: never;
            header: {
                /** @description A step-up token from POST /v1/auth/step-up; each works once. */
                "X-Step-Up-Token": components["parameters"]["StepUp"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @example ETH-SEPOLIA */
                    network: string;
                    address: string;
                    label?: string;
                };
            };
        };
        responses: {
            /** @description The entry. */
            201: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WithdrawAddress"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    deleteWithdrawAddress: {
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
            /** @description Removed. */
            204: {
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
                        items: components["schemas"]["Withdrawal"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    requestWithdrawal: {
        parameters: {
            query?: never;
            header: {
                /** @description A step-up token from POST /v1/auth/step-up; each works once. */
                "X-Step-Up-Token": components["parameters"]["StepUp"];
                "Idempotency-Key"?: string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @example ETH */
                    asset: string;
                    /** @example ETH-SEPOLIA */
                    network: string;
                    address: string;
                    amount: components["schemas"]["Decimal"];
                };
            };
        };
        responses: {
            /** @description Frozen and scored; APPROVED or PENDING_REVIEW. */
            201: {
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
    getWithdrawal: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["WithdrawalID"];
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
                    "application/json": components["schemas"]["Withdrawal"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    cancelWithdrawal: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                id: components["parameters"]["WithdrawalID"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The withdrawal, CANCELED. */
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
}
