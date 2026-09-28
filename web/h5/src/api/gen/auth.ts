// Generated from api/openapi/auth.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/auth/otp/request": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Send a one-time code
         * @description Requires a human-verification token (Cloudflare Turnstile). REGISTER,
         *     LOGIN and PASSWORD_RESET answer identically whether the account
         *     exists; a code only arrives when the action is possible. SMS is
         *     available only where the auth.sms feature flag allows it.
         *     Quotas: one code per target every 60 s, 5 per hour and 10 per day;
         *     20 per hour per IP; 10 per hour per device. STEP_UP, BIND_IDENTITY,
         *     REBIND_IDENTITY and WITHDRAW_CONFIRM need the access token.
         */
        post: operations["requestOtp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/otp/verify": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Check a one-time code and get a ticket
         * @description Five wrong codes lock the challenge (AUTH_OTP_ATTEMPTS_EXCEEDED); a
         *     code expires after 5 minutes (AUTH_OTP_EXPIRED). The ticket works
         *     once, for 5 minutes, on the same device and scene.
         */
        post: operations["verifyOtp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/terms": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Document versions registration must accept */
        get: operations["getTerms"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/register/complete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Create the account behind a REGISTER ticket and sign in
         * @description Passwords have 10 to 128 characters and must not be common or
         *     contain the email or phone number (AUTH_PASSWORD_WEAK). Outdated
         *     document versions fail with AUTH_TERMS_OUTDATED, whose details
         *     carry the current ones. The region comes from the phone number, or
         *     from `country` for email registrations.
         */
        post: operations["register"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/login/password": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Sign in with a password
         * @description Failures count per identifier: from the third a captcha_token is
         *     required (AUTH_CAPTCHA_REQUIRED), the tenth locks it for 15 minutes
         *     (AUTH_ACCOUNT_LOCKED). Unknown accounts fail exactly like wrong
         *     passwords. After 7 days without a successful login the response is
         *     403 AUTH_LOGIN_CHALLENGE_REQUIRED with details.login_challenge_id
         *     and details.channels: request a LOGIN_CHALLENGE code and finish at
         *     /v1/auth/login/challenge.
         */
        post: operations["loginPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/login/complete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Sign in with a LOGIN ticket */
        post: operations["loginOtp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/login/challenge": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Finish a password login with a LOGIN_CHALLENGE ticket */
        post: operations["completeLoginChallenge"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/password/reset/request": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** Send a PASSWORD_RESET code (otp/request with that scene) */
        post: operations["requestPasswordReset"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/password/reset/complete": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Set a new password with a PASSWORD_RESET ticket
         * @description Ends every session. Withdrawals are held for review for 24 hours afterwards.
         */
        post: operations["resetPassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/token/refresh": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Rotate the refresh token and get a new access token
         * @description Browsers send the rt cookie and an allowed Origin; apps send
         *     X-Client-Type: APP and the token in the body. Presenting a rotated
         *     token again revokes the session (AUTH_SESSION_REVOKED), except
         *     within 5 seconds of its rotation (two tabs racing), which answers
         *     AUTH_TOKEN_EXPIRED: retry with the newer cookie.
         */
        post: operations["refreshToken"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/logout": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** End the current session */
        post: operations["logout"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/logout/all": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /** End every other session */
        post: operations["logoutOthers"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/sessions": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** List live sessions (devices) */
        get: operations["listSessions"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/sessions/{session_id}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        post?: never;
        /**
         * End one session
         * @description Ending another device's session needs a step-up; ending the current one is a logout.
         */
        delete: operations["revokeSession"];
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/login-history": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** Login history, newest first */
        get: operations["listLoginHistory"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/step-up": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Trade a STEP_UP ticket for a step-up token
         * @description The token is valid 10 minutes for one sensitive action (§6.5).
         */
        post: operations["stepUp"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/password/change": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Change the password
         * @description Needs a step-up and the current password; ends every other session.
         */
        post: operations["changePassword"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/identity/bind": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Bind a second identity with a BIND_IDENTITY ticket
         * @description One email and one phone per account (AUTH_IDENTITY_KIND_BOUND); needs a step-up.
         */
        post: operations["bindIdentity"];
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/auth/identity/rebind": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Replace an identity with a REBIND_IDENTITY ticket
         * @description With both identities bound, the step-up must come from the other
         *     identity. A single-identity account cannot rebind alone: the
         *     request waits for review (202 PENDING_REVIEW).
         */
        post: operations["rebindIdentity"];
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
        /** @enum {string} */
        Scene: "REGISTER" | "LOGIN" | "LOGIN_CHALLENGE" | "PASSWORD_RESET" | "BIND_IDENTITY" | "REBIND_IDENTITY" | "STEP_UP" | "WITHDRAW_CONFIRM";
        /** @enum {string} */
        Channel: "EMAIL" | "SMS";
        DeviceId: string;
        OtpRequest: {
            scene: components["schemas"]["Scene"];
            channel: components["schemas"]["Channel"];
            /** @description Email address, or phone number with country code (+8613812341234). Omitted for STEP_UP, WITHDRAW_CONFIRM and LOGIN_CHALLENGE. */
            identifier?: string;
            /**
             * Format: uuid
             * @description For LOGIN_CHALLENGE.
             */
            login_challenge_id?: string;
            captcha_token: string;
            device_id: components["schemas"]["DeviceId"];
            /**
             * @description Language of the message; defaults to Accept-Language, then zh-CN.
             * @example zh-CN
             * @example en
             */
            language?: string;
        };
        Challenge: {
            /** Format: uuid */
            challenge_id: string;
            /** Format: date-time */
            expires_at: string;
            /**
             * @description Always QUEUED; codes are sent in the background.
             * @enum {string}
             */
            delivery: "QUEUED";
        };
        OtpVerify: {
            /** Format: uuid */
            challenge_id: string;
            code: string;
            device_id: components["schemas"]["DeviceId"];
        };
        OtpTicket: {
            otp_ticket: string;
            scene: components["schemas"]["Scene"];
            /** Format: date-time */
            expires_at: string;
        };
        Tokens: {
            /** Format: uuid */
            user_id: string;
            /** Format: uuid */
            session_id: string;
            /** @enum {string} */
            token_type: "Bearer";
            access_token: string;
            /** @description Seconds until the access token expires. */
            expires_in: number;
            /** Format: date-time */
            expires_at: string;
            /**
             * @description read for FROZEN accounts, which may only read.
             * @enum {string}
             */
            scope: "full" | "read";
            /** @description APP clients only. */
            refresh_token?: string;
            /** Format: date-time */
            refresh_expires_at: string;
        };
        RegisterRequest: {
            otp_ticket: string;
            password: string;
            /**
             * @description ISO 3166-1 alpha-2; required for email registrations.
             * @example SG
             */
            country?: string;
            language?: string;
            /** @example Asia/Singapore */
            timezone?: string;
            terms_version: string;
            risk_disclosure_version: string;
            device_id: components["schemas"]["DeviceId"];
        };
        PasswordLoginRequest: {
            /** @description Email address or phone number with country code. */
            identifier: string;
            password: string;
            /** @description Required after three failures. */
            captcha_token?: string;
            device_id: components["schemas"]["DeviceId"];
        };
        TicketRequest: {
            otp_ticket: string;
            /**
             * Format: uuid
             * @description For /v1/auth/login/challenge.
             */
            login_challenge_id?: string;
            device_id: components["schemas"]["DeviceId"];
        };
        Session: {
            /** Format: uuid */
            session_id: string;
            device_id: string;
            /** @enum {string} */
            client_type: "WEB" | "APP";
            user_agent: string;
            /** @description Masked. */
            ip: string;
            /** Format: date-time */
            created_at: string;
            /** Format: date-time */
            last_seen_at: string;
            current: boolean;
        };
        LoginEvent: {
            /** @enum {string} */
            method: "PASSWORD" | "OTP" | "LOGIN_CHALLENGE" | "REGISTER";
            /** @enum {string} */
            result: "SUCCESS" | "FAILED_PASSWORD" | "LOCKED" | "CHALLENGE_REQUIRED";
            /** @description Masked email or phone. */
            identity: string;
            device_id: string;
            user_agent: string;
            /** @description Masked. */
            ip: string;
            new_device: boolean;
            /** Format: date-time */
            created_at: string;
        };
        RebindResult: {
            /** @enum {string} */
            status: "DONE" | "PENDING_REVIEW";
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
        /** @description Signed in. WEB clients also receive the rt cookie (HttpOnly, Secure, SameSite=Strict, Path=/v1/auth/token/refresh). */
        Tokens: {
            headers: {
                "Set-Cookie"?: string;
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Tokens"];
            };
        };
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
        /** @description APP receives the refresh token in the body; anything else is WEB (cookie). */
        ClientType: "WEB" | "APP";
        /** @description From POST /v1/auth/step-up; missing or used tokens fail with AUTH_STEP_UP_REQUIRED. */
        StepUpToken: string;
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
    requestOtp: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["OtpRequest"];
            };
        };
        responses: {
            /** @description Challenge created; the code is on its way. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Challenge"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    verifyOtp: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["OtpVerify"];
            };
        };
        responses: {
            /** @description Code accepted. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["OtpTicket"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getTerms: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Current versions. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        terms_version: string;
                        risk_disclosure_version: string;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    register: {
        parameters: {
            query?: never;
            header?: {
                /** @description APP receives the refresh token in the body; anything else is WEB (cookie). */
                "X-Client-Type"?: components["parameters"]["ClientType"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["RegisterRequest"];
            };
        };
        responses: {
            201: components["responses"]["Tokens"];
            default: components["responses"]["Error"];
        };
    };
    loginPassword: {
        parameters: {
            query?: never;
            header?: {
                /** @description APP receives the refresh token in the body; anything else is WEB (cookie). */
                "X-Client-Type"?: components["parameters"]["ClientType"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["PasswordLoginRequest"];
            };
        };
        responses: {
            200: components["responses"]["Tokens"];
            default: components["responses"]["Error"];
        };
    };
    loginOtp: {
        parameters: {
            query?: never;
            header?: {
                /** @description APP receives the refresh token in the body; anything else is WEB (cookie). */
                "X-Client-Type"?: components["parameters"]["ClientType"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TicketRequest"];
            };
        };
        responses: {
            200: components["responses"]["Tokens"];
            default: components["responses"]["Error"];
        };
    };
    completeLoginChallenge: {
        parameters: {
            query?: never;
            header?: {
                /** @description APP receives the refresh token in the body; anything else is WEB (cookie). */
                "X-Client-Type"?: components["parameters"]["ClientType"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TicketRequest"];
            };
        };
        responses: {
            200: components["responses"]["Tokens"];
            default: components["responses"]["Error"];
        };
    };
    requestPasswordReset: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["OtpRequest"];
            };
        };
        responses: {
            /** @description Challenge created. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Challenge"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    resetPassword: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    otp_ticket: string;
                    new_password: string;
                    device_id: components["schemas"]["DeviceId"];
                };
            };
        };
        responses: {
            /** @description Password reset. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    refreshToken: {
        parameters: {
            query?: never;
            header?: {
                /** @description APP receives the refresh token in the body; anything else is WEB (cookie). */
                "X-Client-Type"?: components["parameters"]["ClientType"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody?: {
            content: {
                "application/json": {
                    /** @description APP clients only. */
                    refresh_token?: string;
                    device_id?: components["schemas"]["DeviceId"];
                };
            };
        };
        responses: {
            200: components["responses"]["Tokens"];
            default: components["responses"]["Error"];
        };
    };
    logout: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Signed out. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    logoutOthers: {
        parameters: {
            query?: never;
            header?: {
                /** @description From POST /v1/auth/step-up; missing or used tokens fail with AUTH_STEP_UP_REQUIRED. */
                "X-Step-Up-Token"?: components["parameters"]["StepUpToken"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Other sessions ended. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    listSessions: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Sessions, most recently seen first. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        sessions: components["schemas"]["Session"][];
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    revokeSession: {
        parameters: {
            query?: never;
            header?: {
                /** @description From POST /v1/auth/step-up; missing or used tokens fail with AUTH_STEP_UP_REQUIRED. */
                "X-Step-Up-Token"?: components["parameters"]["StepUpToken"];
            };
            path: {
                session_id: string;
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description Session ended. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    listLoginHistory: {
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
                        items: components["schemas"]["LoginEvent"][];
                        next_cursor: string | null;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    stepUp: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TicketRequest"];
            };
        };
        responses: {
            /** @description Step-up token. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": {
                        step_up_token: string;
                        /** Format: date-time */
                        expires_at: string;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
    changePassword: {
        parameters: {
            query?: never;
            header?: {
                /** @description From POST /v1/auth/step-up; missing or used tokens fail with AUTH_STEP_UP_REQUIRED. */
                "X-Step-Up-Token"?: components["parameters"]["StepUpToken"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    current_password: string;
                    new_password: string;
                };
            };
        };
        responses: {
            /** @description Password changed. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    bindIdentity: {
        parameters: {
            query?: never;
            header?: {
                /** @description From POST /v1/auth/step-up; missing or used tokens fail with AUTH_STEP_UP_REQUIRED. */
                "X-Step-Up-Token"?: components["parameters"]["StepUpToken"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TicketRequest"];
            };
        };
        responses: {
            /** @description Identity bound. */
            204: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    rebindIdentity: {
        parameters: {
            query?: never;
            header?: {
                /** @description From POST /v1/auth/step-up; missing or used tokens fail with AUTH_STEP_UP_REQUIRED. */
                "X-Step-Up-Token"?: components["parameters"]["StepUpToken"];
            };
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": components["schemas"]["TicketRequest"];
            };
        };
        responses: {
            /** @description Identity replaced. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RebindResult"];
                };
            };
            /** @description Waiting for review. */
            202: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["RebindResult"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
