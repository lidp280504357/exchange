// Generated from api/openapi/user.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/user/profile": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** The caller's profile and account status */
        get: operations["getProfile"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        /**
         * Change language, time zone or anti-phishing code
         * @description Omitted fields stay. Changing the anti-phishing code (an empty
         *     string clears it) needs a step-up token; FROZEN accounts may not
         *     change anything (USER_FROZEN).
         */
        patch: operations["updateProfile"];
        trace?: never;
    };
    "/v1/user/eligibility": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * Whether the caller may use a feature now
         * @description Decided by the account status (§5.4 matrix), then the feature's
         *     switch with its region rules. Refusals carry USER_RISK_REVIEW,
         *     USER_FROZEN, USER_CLOSED, USER_REGION_NOT_ALLOWED or
         *     USER_NOT_ELIGIBLE (feature switched off).
         */
        get: operations["getEligibility"];
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
        Profile: {
            /** Format: uuid */
            user_id: string;
            /** @enum {string} */
            status: "ACTIVE" | "RISK_REVIEW" | "FROZEN" | "CLOSED";
            /** @description ISO 3166-1 alpha-2. */
            region: string;
            language: string;
            timezone: string;
            /** @description Empty until set; shown in every mail. */
            anti_phishing_code: string;
            kyc_level: number;
            /** Format: int64 */
            version: number;
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
    getProfile: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Profile. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Profile"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    updateProfile: {
        parameters: {
            query?: never;
            header?: {
                /** @description Required when anti_phishing_code is present. */
                "X-Step-Up-Token"?: string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /**
                     * @example en
                     * @example zh-CN
                     */
                    language?: string;
                    /** @example Asia/Singapore */
                    timezone?: string;
                    anti_phishing_code?: string;
                };
            };
        };
        responses: {
            /** @description Updated profile. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Profile"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getEligibility: {
        parameters: {
            query: {
                feature: "SPOT_TRADE" | "DERIVATIVES_TRADE" | "DEPOSIT" | "WITHDRAW" | "TRANSFER";
                asset?: string;
                symbol?: string;
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Decision. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        feature: string;
                        allowed: boolean;
                        reason_code?: string;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
