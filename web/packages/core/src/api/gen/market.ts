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
    "/v1/market/assets/{code}/logo": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * An asset's logo
         * @description PNG, SVG (cleaned on upload) or WebP. Asked for with the current profile version (v, as logo_url carries it) it is cached for a year; with another version, for a minute.
         */
        get: operations["getAssetLogo"];
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
    "/v1/market/contracts": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * USDT perpetual contracts (delisted ones excluded)
         * @description Linear perpetuals settled in USDT (requirements §5.8, §11.7):
         *     quantities in the base asset; price, margin, fees and PnL in USDT.
         *     A position's leverage and maintenance margin rate come from the
         *     risk tier its notional falls in.
         */
        get: operations["listContracts"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/contracts/{symbol}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** One perpetual contract, delisted ones included */
        get: operations["getContract"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/tickers": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Rolling 24-hour tickers of every listed pair and contract */
        get: operations["listTickers"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/summary": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Top movers and most traded USDT pairs
         * @description The home page overview, from the tickers of the USDT pairs that are
         *     trading and have a price: gainers by 24-hour change (highest
         *     first), losers (lowest first) and turnover by quote volume
         *     (highest first).
         */
        get: operations["getMarketSummary"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/sparklines": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Trend lines of many pairs at once
         * @description The market lists' lines from hourly closes, oldest first: 7d thins
         *     the last week to 56 points, 24h gives the last 24 closes. One
         *     request serves a page of rows; the server keeps each pair's closes
         *     for five minutes. A pair without candles is left out.
         */
        get: operations["getSparklines"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/{symbol}/ticker": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Rolling 24-hour ticker of a pair */
        get: operations["getTicker"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/{symbol}/depth": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Order book depth, aggregated by price, best first
         * @description The engine's latest snapshot (at most every 100 ms); empty before the first one.
         */
        get: operations["getDepth"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/{symbol}/trades": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The latest trades of a pair, newest first */
        get: operations["listTrades"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/{symbol}/mark-price": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A contract's mark and index prices and funding estimate
         * @description Computed every second (requirements §11.7): the index is the
         *     weighted median of reference spot prices; the mark price is the
         *     index moved by the 30-second EMA of the book's mid price relative
         *     to it, at most 1% either way; the funding rate is the running
         *     estimate of the period ending next_funding_time. Prices stay at the
         *     last computation while none is possible; after 10 seconds without
         *     one the contract is degraded (reduce-only trading).
         */
        get: operations["getMarkPrice"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/{symbol}/funding-rates": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * A contract's settled funding rates, newest first
         * @description One per funding period (00:00, 08:00, 16:00 UTC for an 8-hour
         *     interval): clamp(premium + clamp(interest - premium, +-0.05%),
         *     +-cap), premium being the period's average premium index. Positions
         *     at funding_time pay notional x rate at mark_price: longs pay shorts
         *     when the rate is positive.
         */
        get: operations["listFundingRates"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/market/{symbol}/candles": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Candles of a pair, oldest first
         * @description Aligned to UTC (weeks start on Monday). A pair in reference mode
         *     (market.reference_kline) shows its reference market's candles; the
         *     platform's own are built from its trades: intervals without trades
         *     repeat the previous close with zero volume, so the series has no
         *     gaps, and intervals before the first trade are left out. At most
         *     `limit` candles, the latest ones opening before `to`. To page back
         *     through history, pass the oldest open_time received as `to`.
         */
        get: operations["listCandles"];
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
            /** @description The name users see, e.g. TRC20, BEP20, ERC20, Bitcoin; the network code when none is set. */
            display_name: string;
            /** @description Chain identifier, e.g. the EVM chain ID. */
            chain: string;
            /**
             * @description How addresses are written and checked.
             * @enum {string}
             */
            address_format: "EVM" | "TRON" | "BTC";
            /** @description Empty for the chain's native coin. */
            contract_address: string;
            confirmations: number;
            /** @description Usual minutes from the transfer to the credit; 0 when unknown. */
            eta_minutes: number;
            /**
             * @description Block explorer link of a transaction, with a {tx} placeholder.
             * @example https://sepolia.etherscan.io/tx/{tx}
             */
            explorer_tx_url: string | null;
            /** @description Block explorer link of an address, with an {address} placeholder. */
            explorer_address_url: string | null;
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
            /** @description Market-cap rank at listing; null when unranked. */
            rank: number | null;
            /** @description Sector tags, e.g. layer-1, defi, meme. */
            categories: string[];
            /** @description Amounts of the asset have at most this many decimal places. */
            decimals: number;
            deposit_enabled: boolean;
            withdraw_enabled: boolean;
            trading_enabled: boolean;
            networks: components["schemas"]["Network"][];
            /** @description The name operators set for the sites (ASTRA design §5.3); null shows the name. */
            display_name: string | null;
            /** @description Introductions by language ("zh-CN", "en"), set by operators; empty when none. */
            description: {
                [key: string]: string;
            };
            /** @description https links by kind ("website", "explorer", "whitepaper"); empty when none. */
            links: {
                [key: string]: string;
            };
            /** @description The logo (GET /v1/market/assets/{code}/logo?v=...): the URL changes with every profile change, so it can be cached for good. Null without a logo (the sites draw the letter icon). */
            logo_url: string | null;
            /** @description Goes up with every change of the profile. */
            profile_version: number;
        };
        RiskTier: {
            /** @description Largest position notional (USDT) of the tier. */
            max_notional: string;
            max_leverage: number;
            /** @description Maintenance margin rate, liquidation fee included. */
            mmr: string;
        };
        Contract: {
            /** @example BTC-USDT-PERP */
            symbol: string;
            /** @enum {string} */
            type: "PERPETUAL";
            base_asset: string;
            quote_asset: string;
            /** @description The spot market whose reference prices make the index. */
            index_symbol: string;
            tick_size: string;
            lot_size: string;
            min_quantity: string;
            max_quantity: string;
            min_notional: string;
            /** @description Largest deviation of a limit price from the mark price, as a fraction. */
            price_band: string;
            max_leverage: number;
            risk_tiers: components["schemas"]["RiskTier"][];
            /** @enum {integer} */
            funding_interval_hours: 1 | 4 | 8;
            /** @description Per funding interval. */
            interest_rate: string;
            funding_cap: string;
            impact_notional: string;
            maker_fee_rate: string;
            taker_fee_rate: string;
            /** @enum {string} */
            status: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
        };
        TradingPair: {
            /** @example BTC-USDT */
            symbol: string;
            base_asset: string;
            quote_asset: string;
            /** @description The base asset's name, e.g. Bitcoin. */
            base_name: string;
            /** @description The base asset's market-cap rank at listing; null when unranked. */
            rank: number | null;
            /** @description The base asset's sector tags. */
            categories: string[];
            tick_size: components["schemas"]["Decimal"];
            lot_size: components["schemas"]["Decimal"];
            /** @description Decimal places of tick_size, for display. */
            price_decimals: number;
            /** @description Decimal places of lot_size, for display. */
            qty_decimals: number;
            min_quantity: components["schemas"]["Decimal"];
            max_quantity: components["schemas"]["Decimal"];
            min_notional: components["schemas"]["Decimal"];
            price_band: components["schemas"]["Decimal"];
            maker_fee_rate: components["schemas"]["Decimal"];
            taker_fee_rate: components["schemas"]["Decimal"];
            /** @enum {string} */
            status: "PREPARE" | "TRADING" | "HALT" | "CANCEL_ONLY" | "DELISTED";
            /**
             * @description The reference market the pair's market data follows (ADR-0010),
             *     e.g. BTCUSDT; null when the pair shows its own trades and book.
             */
            reference_symbol: string | null;
            /** @description Platform price = reference price x multiplier (1000 for a 1000PEPE pair); quantities divide by it. */
            reference_multiplier: components["schemas"]["Decimal"];
            /** Format: date-time */
            listed_at: string;
            /** @description The base asset's display name (Asset.display_name); null shows base_name. */
            base_display_name: string | null;
            /** @description The base asset's logo (Asset.logo_url); null without one. */
            base_logo_url: string | null;
        };
        NullableDecimal: string | null;
        /**
         * @description Rolling 24 hours (§11.8): the reference market's while
         *     market.reference_ticker is on for the symbol (ADR-0010), else the
         *     platform's at minute resolution. Prices are null while unknown: a
         *     pair that never traded, or an empty side of the book. A reference
         *     ticker keeps its updated_at when the feed stalls: clients show a
         *     warning once it is 30 seconds old.
         */
        Ticker: {
            symbol: string;
            /** @description The base asset's market-cap rank; null when unranked. */
            rank: number | null;
            last: components["schemas"]["NullableDecimal"];
            /** @description The last trade price 24 hours ago. */
            open: components["schemas"]["NullableDecimal"];
            high: components["schemas"]["NullableDecimal"];
            low: components["schemas"]["NullableDecimal"];
            volume: components["schemas"]["Decimal"];
            quote_volume: components["schemas"]["Decimal"];
            /** Format: int64 */
            trade_count: number;
            /** @description (last - open) / open as a fraction, e.g. "0.0125" for 1.25%. */
            change: components["schemas"]["NullableDecimal"];
            bid: components["schemas"]["NullableDecimal"];
            ask: components["schemas"]["NullableDecimal"];
            /**
             * Format: date-time
             * @description When the reference source computed the ticker, or now for the platform's.
             */
            updated_at: string;
            /**
             * Format: date-time
             * @description When the platform's own last trade was, how fresh last is; null for a reference ticker or before any trade.
             */
            last_trade_at?: string | null;
        };
        MarkPrice: {
            symbol: string;
            /** @description The spot market the index follows, e.g. BTC-USDT. */
            index_symbol: string;
            /** @description Null before the first computation. */
            mark_price: components["schemas"]["NullableDecimal"];
            index_price: components["schemas"]["NullableDecimal"];
            /** @description The running period's estimate, e.g. "0.0001" for 0.01%. */
            funding_rate: components["schemas"]["Decimal"];
            interest_rate: components["schemas"]["Decimal"];
            /** Format: date-time */
            next_funding_time: string;
            /** @description No mark price for 10 seconds; trading is reduce-only until an operator lifts it. */
            degraded: boolean;
            /**
             * Format: date-time
             * @description When the prices were computed.
             */
            updated_at: string | null;
        };
        FundingRate: {
            /** Format: date-time */
            funding_time: string;
            funding_rate: components["schemas"]["Decimal"];
            mark_price: components["schemas"]["Decimal"];
            index_price: components["schemas"]["Decimal"];
            /** @description The period's average premium index. */
            premium: components["schemas"]["Decimal"];
            interest_rate: components["schemas"]["Decimal"];
            /**
             * Format: int64
             * @description Premium index samples in the period (one a second).
             */
            samples: number;
            /** Format: date-time */
            settled_at: string;
        };
        /** @description [price, quantity] */
        PriceLevel: components["schemas"]["Decimal"][];
        PublicTrade: {
            /** Format: uuid */
            trade_id: string;
            /**
             * Format: int64
             * @description The pair's trades counted from 1 (0 for trades from before numbering).
             */
            trade_number: number;
            price: components["schemas"]["Decimal"];
            quantity: components["schemas"]["Decimal"];
            quote_quantity: components["schemas"]["Decimal"];
            /** @enum {string} */
            taker_side: "BUY" | "SELL";
            /** Format: date-time */
            executed_at: string;
        };
        Candle: {
            /** Format: date-time */
            open_time: string;
            open: components["schemas"]["Decimal"];
            high: components["schemas"]["Decimal"];
            low: components["schemas"]["Decimal"];
            close: components["schemas"]["Decimal"];
            volume: components["schemas"]["Decimal"];
            quote_volume: components["schemas"]["Decimal"];
            /** Format: int64 */
            trade_count: number;
            /** @description The interval has ended. */
            closed: boolean;
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
        /** @description A listed pair or contract (case-insensitive). */
        Symbol: string;
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
    getAssetLogo: {
        parameters: {
            query?: {
                v?: number;
            };
            header?: never;
            path: {
                code: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The logo. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "image/png": string;
                    "image/svg+xml": string;
                    "image/webp": string;
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
    listContracts: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Every listed contract. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        contracts: components["schemas"]["Contract"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getContract: {
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
            /** @description The contract. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Contract"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listTickers: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description One ticker per listed pair and contract. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        tickers: components["schemas"]["Ticker"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getMarketSummary: {
        parameters: {
            query?: {
                /** @description Tickers per list. */
                limit?: number;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The overview. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        gainers: components["schemas"]["Ticker"][];
                        losers: components["schemas"]["Ticker"][];
                        turnover: components["schemas"]["Ticker"][];
                        /** Format: date-time */
                        updated_at: string;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getSparklines: {
        parameters: {
            query: {
                /** @description Up to 60 pairs or contracts, comma-separated. */
                symbols: string;
                range?: "7d" | "24h";
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The lines by symbol. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        /** @enum {string} */
                        range: "7d" | "24h";
                        /** @constant */
                        interval: "1h";
                        sparklines: {
                            [key: string]: components["schemas"]["Decimal"][];
                        };
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getTicker: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A listed pair or contract (case-insensitive). */
                symbol: components["parameters"]["Symbol"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The ticker. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Ticker"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getDepth: {
        parameters: {
            query?: {
                /** @description Levels per side, at most 200. */
                limit?: number;
            };
            header?: never;
            path: {
                /** @description A listed pair or contract (case-insensitive). */
                symbol: components["parameters"]["Symbol"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The depth. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        symbol: string;
                        /**
                         * Format: int64
                         * @description The book's engine sequence at the snapshot; 0 before the first.
                         */
                        sequence: number;
                        bids: components["schemas"]["PriceLevel"][];
                        asks: components["schemas"]["PriceLevel"][];
                        /** Format: date-time */
                        updated_at: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listTrades: {
        parameters: {
            query?: {
                limit?: number;
            };
            header?: never;
            path: {
                /** @description A listed pair or contract (case-insensitive). */
                symbol: components["parameters"]["Symbol"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The trades. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        symbol: string;
                        trades: components["schemas"]["PublicTrade"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getMarkPrice: {
        parameters: {
            query?: never;
            header?: never;
            path: {
                /** @description A listed pair or contract (case-insensitive). */
                symbol: components["parameters"]["Symbol"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The prices. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["MarkPrice"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listFundingRates: {
        parameters: {
            query?: {
                /** @description Funding times at or after this (RFC 3339). */
                from?: string;
                /** @description Funding times before this (RFC 3339). */
                to?: string;
                limit?: number;
            };
            header?: never;
            path: {
                /** @description A listed pair or contract (case-insensitive). */
                symbol: components["parameters"]["Symbol"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The rates. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        symbol: string;
                        funding_rates: components["schemas"]["FundingRate"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    listCandles: {
        parameters: {
            query: {
                interval: "1m" | "3m" | "5m" | "15m" | "30m" | "1h" | "2h" | "4h" | "6h" | "12h" | "1d" | "1w" | "1M";
                /** @description First candle, the one containing this time (RFC 3339); default limit intervals before to. */
                from?: string;
                /** @description Candles opening before this time (RFC 3339); default now, which includes the open candle. */
                to?: string;
                limit?: number;
            };
            header?: never;
            path: {
                /** @description A listed pair or contract (case-insensitive). */
                symbol: components["parameters"]["Symbol"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The candles. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        symbol: string;
                        interval: string;
                        candles: components["schemas"]["Candle"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
