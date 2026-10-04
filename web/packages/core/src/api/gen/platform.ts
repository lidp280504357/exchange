// Generated from api/openapi/platform.yaml by scripts/gen-api.mjs; do not edit.

export interface paths {
    "/v1/platform/profile": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The platform's profile
         * @description Cacheable for a minute. The ETag is the profile's version: with If-None-Match the answer is 304 while it has not changed.
         */
        get: operations["getPlatformProfile"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/platform/images/{kind}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * One of the platform's images
         * @description PNG, SVG (cleaned on upload) or WebP, square. Asked for with the current profile version (v, as the profile's image URLs carry it) it is cached for a year; with another version, for a minute. 404 while none is uploaded: the sites show their built-in image.
         */
        get: operations["getPlatformImage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/manifest.webmanifest": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The mobile site's web app manifest
         * @description Built from the profile (name, short name, theme colour, the uploaded icons, else the built-in ones); m.astras.vip serves it at the same path as before. Cacheable for a minute.
         */
        get: operations["getWebManifest"];
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
        /** @enum {string} */
        PlatformImageKind: "logo_light" | "logo_dark" | "favicon" | "apple_touch_icon";
        /** @description A text by language; zh-CN is the fallback of a language without one. */
        Texts: {
            "zh-CN": string;
            en: string;
        };
        WelcomeCredit: {
            /** @example USDT */
            asset: string;
            /**
             * @description Decimal string, greater than 0.
             * @example 10000
             */
            amount: string;
        };
        PlatformProfile: {
            /**
             * @description The exchange's name, 2-32 characters.
             * @example Astras
             */
            name: string;
            /** @description Where space is short (the home screen), 2-12 characters. */
            short_name: string;
            /**
             * @description The PC site's host name (the mobile site is m.<domain>, the console admin.<domain>); empty until set.
             * @example astras.vip
             */
            domain: string;
            /** @description #rrggbb, the theme colour of the browser (meta theme-color, the manifest). */
            theme_color: string;
            /** @description #rrggbb, the brand colour of buttons and highlights (CSS --brand). */
            brand_color: string;
            /** @description Each image's URL with the profile version, null while none is uploaded (the built-in one shows). */
            images: {
                /** @description The mark next to the name on a light background. */
                logo_light: string | null;
                /** @description The mark on a dark background. */
                logo_dark: string | null;
                favicon: string | null;
                apple_touch_icon: string | null;
            };
            footer: {
                copyright: components["schemas"]["Texts"];
                /** @description Registration or compliance lines; empty for none. */
                compliance: components["schemas"]["Texts"];
            };
            contact: {
                /** @description Empty for none. */
                email: string;
                /** @description An https URL. */
                support_url: string | null;
            };
            social: {
                /** @enum {string} */
                kind: "x" | "telegram" | "discord" | "youtube" | "facebook" | "instagram" | "linkedin" | "reddit" | "medium" | "github" | "tiktok" | "weibo";
                /** @description An https URL. */
                url: string;
            }[];
            /** @enum {string} */
            default_locale: "zh-CN" | "en";
            /** @description The exchange in test mode (the learning mode until 2026-10-04; off when live). While enabled the sites show the content marked TEST or BOTH (FORMAL or BOTH when off, design §4.4), "测试模式" badges and, when banner is true, text in a banner at the top. */
            test_mode: {
                enabled: boolean;
                /** @description Show the banner while test mode is on (operators may hide it). */
                banner: boolean;
                text: components["schemas"]["Texts"];
            };
            /**
             * @deprecated
             * @description The test mode under its former name (enabled and text), until the admin console reads test_mode.
             */
            learning_mode: {
                enabled: boolean;
                text: components["schemas"]["Texts"];
            };
            /** @description CLOSED refuses sign-ups (403 AUTH_REGISTRATION_CLOSED) and the sign-up pages show closed_text. */
            registration: {
                /** @enum {string} */
                status: "OPEN" | "CLOSED";
                closed_text: components["schemas"]["Texts"];
            };
            /** @description What a new account gets (the ledger's setting, read within the last minute); empty for nothing. */
            welcome_credits: components["schemas"]["WelcomeCredit"][];
            /**
             * Format: int64
             * @description Goes up with every change; the ETag and the image URLs carry it.
             */
            version: number;
            /** Format: date-time */
            updated_at: string;
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
    getPlatformProfile: {
        parameters: {
            query?: never;
            header?: {
                "If-None-Match"?: string;
            };
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The profile. */
            200: {
                headers: {
                    ETag?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformProfile"];
                };
            };
            /** @description Not changed since the ETag sent. */
            304: {
                headers: {
                    [name: string]: unknown;
                };
                content?: never;
            };
            default: components["responses"]["Error"];
        };
    };
    getPlatformImage: {
        parameters: {
            query?: {
                v?: number;
            };
            header?: never;
            path: {
                kind: components["schemas"]["PlatformImageKind"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The image. */
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
    getWebManifest: {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The manifest. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/manifest+json": {
                        [key: string]: unknown;
                    };
                };
            };
            default: components["responses"]["Error"];
        };
    };
}
