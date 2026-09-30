// Generated from api/openapi/notification.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/notifications": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's notifications, newest first */
        get: operations["listNotifications"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/notifications/read": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Mark notifications read */
        post: operations["markNotificationsRead"];
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
        Notification: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            type: "WELCOME" | "NEW_DEVICE_LOGIN" | "IDENTITY_CHANGED" | "PASSWORD_CHANGED" | "ACCOUNT_LOCKED" | "STATUS_CHANGED" | "TOTP_CHANGED" | "DEPOSIT_CREDITED" | "DEPOSIT_UNCLAIMED" | "WITHDRAWAL_REQUESTED" | "WITHDRAWAL_COMPLETED" | "WITHDRAWAL_REJECTED" | "WITHDRAWAL_CANCELED" | "WITHDRAWAL_FAILED";
            title: string;
            /** @description Rendered in the user's language and time zone. */
            body: string;
            /** @description Masked specifics, e.g. ip, channel, to. */
            data: {
                [key: string]: string;
            };
            read: boolean;
            /** Format: date-time */
            created_at: string;
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
    listNotifications: {
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
            /** @description One page and the unread count. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        items: components["schemas"]["Notification"][];
                        next_cursor: string | null;
                        unread_count: number;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    markNotificationsRead: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    ids?: string[];
                    all?: boolean;
                };
            };
        };
        responses: {
            /** @description How many changed. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        updated: number;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
