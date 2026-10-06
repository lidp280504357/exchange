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
    "/v1/user/username": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        /**
         * Change the caller's username
         * @description 3 to 20 letters, digits or underscores, not starting with an
         *     underscore (USER_USERNAME_INVALID, as are a few reserved names:
         *     admin, support, astras, house and the like); unique whatever the
         *     case (USER_USERNAME_TAKEN, 409); once in 7 days
         *     (USER_USERNAME_COOLDOWN, 409, details.next_change_at). The same
         *     name in another case is a change too. FROZEN accounts may not
         *     change it (USER_FROZEN). Kept in the user's security events; no
         *     mail.
         */
        put: operations["changeUsername"];
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/user/avatar": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        get?: never;
        put?: never;
        /**
         * Upload the caller's avatar
         * @description One part, file: a PNG, JPEG or WebP image of at most 5 MB
         *     (USER_AVATAR_TOO_LARGE, 413; the request at most 8 MB), each side
         *     at least 64 pixels. Its content decides the format, not its name or
         *     type: anything else, or an image that does not decode, is
         *     USER_AVATAR_INVALID (SVG and animated images too). The middle
         *     square is kept, scaled to 256 x 256 and 64 x 64 WebP without any
         *     metadata (EXIF and the like); the original is not kept, and the
         *     previous avatar's files are deleted. FROZEN accounts may not upload
         *     (USER_FROZEN). No Idempotency-Key: a repeat stores a new file.
         */
        post: operations["uploadAvatar"];
        /**
         * Go back to the default avatar
         * @description The uploaded files are deleted; avatar_url and avatar_thumb_url become null. Without one it changes nothing.
         */
        delete: operations["deleteAvatar"];
        options?: never;
        head?: never;
        patch?: never;
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
    "/v1/user/favorites": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The caller's favorite markets
         * @description In the caller's order; empty, with a null updated_at, until first set.
         */
        get: operations["getFavorites"];
        /**
         * Replace the caller's favorite markets
         * @description Clients keep favorites locally and merge them into this list after
         *     signing in. Symbols are upper-cased and repeats dropped, keeping the
         *     first; they are not checked against the listing.
         */
        put: operations["setFavorites"];
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
            /**
             * @description What the sites show and call the user by (design 2026-10-07 §1):
             *     user_ and 8 lowercase letters or digits at sign-up, changeable
             *     once in 7 days (PUT /v1/user/username). Signing in stays with
             *     the email or phone.
             * @example user_k3x9q2ab
             */
            username: string;
            /**
             * Format: date-time
             * @description When the user last changed it; null while it is the one given at sign-up (or by an operator's reset).
             */
            username_changed_at: string | null;
            /**
             * @description The uploaded avatar, 256 x 256 WebP, a path every site serves
             *     (/uploads/avatars/<user_id>/<random>.webp; a new upload gets a
             *     new name). Null for the default: one of the sites' 12 built-in
             *     images, chosen by the user ID.
             * @example /uploads/avatars/0192f0c4-8a3e-7b2d-9c1f-3e5a7d9b1c2e/k3x9q2ab5d7f.webp
             */
            avatar_url: string | null;
            /** @description The same at 64 x 64; null with avatar_url. */
            avatar_thumb_url: string | null;
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
        Favorites: {
            symbols: string[];
            /** Format: date-time */
            updated_at: string | null;
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
                     * @description The language of mails and notices; en, zh-TW (and zh-HK, zh-Hant) for Traditional Chinese, Simplified Chinese otherwise.
                     * @example en
                     * @example zh-CN
                     * @example zh-TW
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
    changeUsername: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    /** @example satoshi_n */
                    username: string;
                };
            };
        };
        responses: {
            /** @description The profile with the new name. */
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
    uploadAvatar: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "multipart/form-data": {
                    /** Format: binary */
                    file: string;
                };
            };
        };
        responses: {
            /** @description The profile with the new avatar's URLs. */
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
    deleteAvatar: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The profile with the default avatar. */
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
                /** @description COIN_M_TRADE is trading the coin-margined perpetuals (derivatives.coin_m, design 2026-10-06 §2.5). */
                feature: "SPOT_TRADE" | "DERIVATIVES_TRADE" | "DEPOSIT" | "WITHDRAW" | "TRANSFER" | "MARGIN_TRADE" | "COIN_M_TRADE";
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
    getFavorites: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The favorites. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Favorites"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    setFavorites: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody: {
            content: {
                "application/json": {
                    symbols: string[];
                };
            };
        };
        responses: {
            /** @description The favorites as stored. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Favorites"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
