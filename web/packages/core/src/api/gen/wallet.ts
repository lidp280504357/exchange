// Generated from api/openapi/wallet.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/wallet/networks": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The networks an asset moves on
         * @description What the deposit and withdrawal pages show of each network: its
         *     name, address format, confirmations, usual time to a credit,
         *     minimums, withdrawal fee and explorer links. USDT has one balance
         *     whatever network it arrives on; the network is chosen per deposit
         *     address and per withdrawal.
         */
        get: operations["listWalletNetworks"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/wallet/limits": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The caller's withdrawal limits in effect
         * @description The daily and monthly limits in USDT the next withdrawal is checked
         *     against, what is used of them (today and this month, UTC), and the
         *     full limits: both identities (email and phone) and an authenticator
         *     app give them, the app only once it has been bound for
         *     totp_settling_hours (24); until then full_limits_at says when.
         *     The pages read the numbers and the hours from here.
         */
        get: operations["getWithdrawLimits"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/wallet/withdraw-addresses/validate": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Check an address before saving or using it
         * @description Checks the form for the network (EVM: 0x and 40 hex digits, EIP-55
         *     when mixed case; TRON: Base58Check starting with T; Bitcoin:
         *     bech32/bech32m or Base58Check of the right network) and whether it
         *     is a platform deposit address: the caller's own is refused, another
         *     user's makes the withdrawal an internal transfer without a network
         *     fee. The form is all it can tell: a valid address may still belong
         *     to nobody.
         */
        post: operations["validateWithdrawAddress"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
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
         *     WALLET_WITHDRAW_SUSPENDED (the asset's withdrawals are paused while
         *     the platform checks its funds), WALLET_INVALID_ADDRESS,
         *     WALLET_ADDRESS_NOT_WHITELISTED,
         *     WALLET_ADDRESS_COOLDOWN (details.usable_at), WALLET_BELOW_MINIMUM,
         *     WALLET_AMOUNT_PRECISION, WALLET_LIMIT_EXCEEDED (details: the limits
         *     in effect and what was used; while an authenticator app bound
         *     within totp_settling_hours holds the full limits back, also
         *     full_limits_at, full_daily_limit, full_monthly_limit and
         *     totp_settling_hours; GET /v1/wallet/limits says the same before a
         *     request), WALLET_OWN_ADDRESS, AUTH_STEP_UP_REQUIRED and the
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
         * @description Releases the frozen funds. Fails with WALLET_WITHDRAWAL_NOT_CANCELABLE
         *     once it is being signed or is with the custodian.
         */
        delete: operations["cancelWithdrawal"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/wallet/callbacks/udun": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * The custodian's notice of a deposit or withdrawal (Udun)
         * @description Called by the custodian, not by users (ADR-0011). The four fields
         *     come as a form or as JSON: body is the trade as a JSON string,
         *     signed with the merchant key as sign = md5(body + key + nonce +
         *     timestamp); the timestamp (seconds or milliseconds) must be within
         *     five minutes. A trade is applied once per status: tradeType 1 is a
         *     deposit, credited at status 3; tradeType 2 a withdrawal (businessId
         *     = the withdrawal ID), 0 and 1 the custodian's review, 2 refused, 3
         *     sent (settled and CONFIRMED), 4 failed. Every callback is logged
         *     for the admin console. The answer is "success" once the callback is
         *     recorded and applied; anything else asks the custodian to try again.
         */
        post: operations["udunCallback"];
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
        WithdrawLimits: {
            /**
             * @description USDT a day, in effect now.
             * @example 400
             */
            daily_limit: string;
            /** @example 4000 */
            monthly_limit: string;
            used_today: string;
            used_this_month: string;
            /**
             * @description USDT a day with both identities and a settled authenticator app.
             * @example 2000
             */
            full_daily_limit: string;
            /** @example 20000 */
            full_monthly_limit: string;
            /** @description Verified identities (email, phone). */
            identities: number;
            totp_enabled: boolean;
            /**
             * @description How long a newly bound authenticator app waits before it raises the limits.
             * @example 24
             */
            totp_settling_hours: number;
            /**
             * Format: date-time
             * @description When the full limits come while an authenticator app bound with both identities settles; null otherwise.
             */
            full_limits_at: string | null;
        };
        WalletNetwork: {
            asset: string;
            /** @example ETH-SEPOLIA */
            network: string;
            /**
             * @example TRC20
             * @example ERC20
             * @example Sepolia
             */
            display_name: string;
            chain: string;
            /** @enum {string} */
            address_format: "EVM" | "TRON" | "BTC";
            /** @description The token contract; null for the chain's native coin. */
            contract: string | null;
            confirmations: number;
            /** @description Usual minutes from the transfer to the credit; 0 when unknown. */
            eta_minutes: number;
            min_deposit: string;
            min_withdraw: string;
            withdraw_fee: string;
            memo_required: boolean;
            deposit_enabled: boolean;
            /** @description The network takes withdrawals now; false while the asset's withdrawals are suspended. */
            withdraw_enabled: boolean;
            /** @description The asset's withdrawals are suspended while the platform checks its funds (requests fail with WALLET_WITHDRAW_SUSPENDED); deposits go on. */
            withdraw_suspended: boolean;
            /** @description Explorer link of a transaction with a {tx} placeholder. */
            explorer_tx_url: string | null;
            /** @description Explorer link of an address with an {address} placeholder. */
            explorer_address_url: string | null;
        };
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
            /**
             * @description UNKNOWN_ADDRESS: sent to an address no account held (a retired
             *     one, say); an administrator credited it to this account after
             *     finding it is theirs.
             * @enum {string|null}
             */
            reason: "BELOW_MINIMUM" | "ACCOUNT_CLOSED" | "NOT_ELIGIBLE" | "UNSUPPORTED_TOKEN" | "UNKNOWN_ADDRESS" | null;
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
            /** @description EIP-55 checksummed on EVM networks, as the network writes it elsewhere. */
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
            /** @description The custodian sends it (SUBMITTED until it reports); otherwise the platform signs and broadcasts it. */
            custody: boolean;
            /** @enum {string} */
            status: "REQUESTED" | "PENDING_REVIEW" | "APPROVED" | "SIGNING" | "BROADCAST" | "CONFIRMING" | "SUBMITTED" | "CONFIRMED" | "INTERNAL_TRANSFER" | "REJECTED" | "CANCELED" | "FAILED";
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
            /**
             * Format: date-time
             * @description When it was handed to the custodian.
             */
            submitted_at: string | null;
            /** Format: date-time */
            broadcast_at: string | null;
            /** Format: date-time */
            confirmed_at: string | null;
        };
        CustodyEnvelope: {
            /** @description Unix time in seconds or milliseconds (a number in JSON). */
            timestamp: string;
            /** @description The custodian's random number (a number in JSON). */
            nonce: string;
            /** @description md5(body + key + nonce + timestamp), lower-case hex. */
            sign: string;
            /**
             * @description The trade as JSON: address, amount and fee (integers in units of
             *     10^-decimals), decimals, mainCoinType, coinType, businessId,
             *     blockHigh, status, tradeId, tradeType, txId, memo.
             */
            body: string;
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
    listWalletNetworks: {
        parameters: {
            query?: {
                /** @description The asset (case-insensitive); every asset's networks when omitted. */
                asset?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The networks, closed ones included (see the switches). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        networks: components["schemas"]["WalletNetwork"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getWithdrawLimits: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The limits. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["WithdrawLimits"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    validateWithdrawAddress: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @description Picks the network's asset when several share it. */
                    asset?: string;
                    /** @example ETH-SEPOLIA */
                    network: string;
                    address: string;
                    /** @description Memo or tag, for networks that need one. */
                    memo?: string;
                };
            };
        };
        responses: {
            /** @description The outcome; an invalid address is not an error. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        valid: boolean;
                        network: string;
                        /** @enum {string} */
                        address_format: "EVM" | "TRON" | "BTC";
                        /** @description The address as the network writes it (EIP-55 case for EVM); null when invalid. */
                        normalized: string | null;
                        /**
                         * @description Why it is invalid: not an address of the network's kind,
                         *     a checksum mismatch (a typo), an address of another
                         *     network (e.g. testnet), a missing memo, the caller's
                         *     own deposit address, or a deposit address taken out of
                         *     use (the custodian's stand-in made it up: no chain
                         *     knows it).
                         * @enum {string|null}
                         */
                        reason: "ADDRESS_FORMAT" | "ADDRESS_CHECKSUM" | "ADDRESS_NETWORK" | "MEMO_REQUIRED" | "ADDRESS_OWN" | "ADDRESS_RETIRED" | null;
                        /** @description Another user's deposit address; the withdrawal completes inside the platform with no fee. */
                        internal: boolean;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
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
    udunCallback: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/x-www-form-urlencoded": components["schemas"]["CustodyEnvelope"];
                "application/json": components["schemas"]["CustodyEnvelope"];
            };
        };
        responses: {
            /** @description Recorded and applied (or already applied before). */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "text/plain": "success";
                };
            };
            /** @description Signature or timestamp wrong (WALLET_CALLBACK_SIGNATURE, WALLET_CALLBACK_STALE); logged and not applied. */
            401: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Error"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
