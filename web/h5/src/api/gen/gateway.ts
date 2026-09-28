// Generated from api/openapi/gateway.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/time": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Server time for client clock sync */
        get: operations["getServerTime"];
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
        ServerTime: {
            /**
             * Format: date-time
             * @description RFC 3339, UTC, millisecond precision.
             * @example 2026-09-28T12:00:00.123Z
             */
            server_time: string;
            /**
             * Format: int64
             * @description Milliseconds since the Unix epoch.
             */
            epoch_ms: number;
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
    getServerTime: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Current server time. */
            200: {
                headers: {
                    "X-Trace-Id": components["headers"]["X-Trace-Id"];
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ServerTime"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
