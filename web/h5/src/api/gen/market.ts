// Generated from api/openapi/market.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/market/assets": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Assets with their networks */
        get: operations["listAssets"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/pairs": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Trading pairs (delisted ones excluded) */
        get: operations["listPairs"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/pairs/{symbol}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** One trading pair, delisted ones included */
        get: operations["getPair"];
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
        /** @example 0.00001 */
        Decimal: string;
        Network: {
            /** @example ETH-SEPOLIA */
            network: string;
            /** @description Chain identifier, e.g. the EVM chain ID. */
            chain: string;
            /** @description Empty for the chain's native coin. */
            contract_address: string;
            confirmations: number;
            min_deposit: components["schemas"]["Decimal"];
            min_withdraw: components["schemas"]["Decimal"];
            withdraw_fee: components["schemas"]["Decimal"];
            memo_required: boolean;
            deposit_enabled: boolean;
            withdraw_enabled: boolean;
        };
        Asset: {
            /** @example BTC */
            asset_code: string;
            name: string;
            /** @description Amounts of the asset have at most this many decimal places. */
            decimals: number;
            deposit_enabled: boolean;
            withdraw_enabled: boolean;
            trading_enabled: boolean;
            networks: components["schemas"]["Network"][];
        };
        TradingPair: {
            /** @example BTC-USDT */
            symbol: string;
            base_asset: string;
            quote_asset: string;
            tick_size: components["schemas"]["Decimal"];
            lot_size: components["schemas"]["Decimal"];
            min_quantity: components["schemas"]["Decimal"];
            max_quantity: components["schemas"]["Decimal"];
            min_notional: components["schemas"]["Decimal"];
            price_band: components["schemas"]["Decimal"];
            maker_fee_rate: components["schemas"]["Decimal"];
            taker_fee_rate: components["schemas"]["Decimal"];
            /** @enum {string} */
            status: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
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
    listAssets: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Every asset. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        assets: components["schemas"]["Asset"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listPairs: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Every listed pair. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        pairs: components["schemas"]["TradingPair"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getPair: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                symbol: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The pair. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["TradingPair"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
