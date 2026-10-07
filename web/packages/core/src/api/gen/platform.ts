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
    "/v1/platform/apps": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The apps to download, Android's and iOS's
         * @description Design 2026-10-07 (App download page) §2.2: each platform's app as
         *     the console set it — a link (an app store, TestFlight, another
         *     page) or a file uploaded in the console and served from
         *     /downloads/ on each of the sites — while it is enabled and
         *     complete; null otherwise (the sites leave the platform out, and
         *     hide the download entries while both are null). Cacheable for a
         *     minute; the ETag is the two platforms' versions, Android's then
         *     iOS's, and the profile's ("3-5-7": every change raises one of them,
         *     a new domain moving the files' addresses included), a strong tag
         *     that If-None-Match matches weakened too (W/"3-5-7", as Cloudflare
         *     sends it) and answers 304 while it has not changed. Batch H0; built
         *     in H1.
         */
        get: operations["getPlatformApps"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/platform/products": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The product lines and whether each is open
         * @description Design 2026-10-07 (product switches) §1 #6: spot trading and the
         *     USDT- and coin-margined contracts, each open unless an operator
         *     closed it in the console (the flags product.spot, product.usdt_m
         *     and product.coin_m; seeded open, never deleted). The sites read it
         *     at start and every minute: a closed line leaves their menus, lists,
         *     searches and boards, its terminals say it is not open, and a user
         *     who still holds its positions or orders keeps a way to close them.
         *     Orders other than cancels and reduce-only closes are refused with
         *     PRODUCT_CLOSED (details.product). Cacheable for 30 seconds; the
         *     ETag is the three flags' versions in the order spot, usdt_m, coin_m
         *     ("3-1-1"; 0 for one not stored), a strong tag that If-None-Match
         *     matches weakened too, and answers 304 while none changed. No
         *     WebSocket push. Batch K0.
         */
        get: operations["getPlatformProducts"];
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
        ProductLine: {
            /** @description Open (true) or closed by an operator. */
            enabled: boolean;
            /**
             * Format: date-time
             * @description When an operator closed it; only while closed.
             */
            closed_at?: string;
        };
        PlatformProducts: {
            spot: components["schemas"]["ProductLine"];
            usdt_m: components["schemas"]["ProductLine"];
            coin_m: components["schemas"]["ProductLine"];
        };
        /** @enum {string} */
        PlatformImageKind: "logo_light" | "logo_dark" | "favicon" | "apple_touch_icon";
        /** @description A text by language; zh-CN is the fallback of a language without one. zh-TW (Traditional Chinese, design 2026-10-06 繁体中文 §2.1) is absent or empty until operators write it. */
        Texts: {
            "zh-CN": string;
            "zh-TW"?: string;
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
            default_locale: "zh-CN" | "zh-TW" | "en";
            /** @description The exchange in test mode (the learning mode until 2026-10-04; off when live). While enabled the sites show the content marked TEST or BOTH (FORMAL or BOTH when off, design §4.4), "测试模式" badges and, when banner is true, text in a banner at the top. */
            test_mode: {
                enabled: boolean;
                /** @description Show the banner while test mode is on (operators may hide it). */
                banner: boolean;
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
        PlatformApps: {
            android: components["schemas"]["AppDownload"] | null;
            ios: components["schemas"]["AppDownload"] | null;
        };
        /**
         * @description One platform's app. A link (mode LINK) has no file facts (null);
         *     an uploaded file (FILE) has them as read from the package when it
         *     was uploaded. URLs are absolute: a file's on https://<the
         *     profile's domain>/downloads/... (any of the three sites serves it).
         */
        AppDownload: {
            /** @enum {string} */
            mode: "LINK" | "FILE";
            /**
             * @description The link, or the file (an .apk to download; an .ipa, which iOS installs through install_url).
             * @example https://astras.vip/downloads/android/0192a000-0000-7000-8000-000000000001.apk
             */
            url: string;
            /** @description An uploaded iOS app's over-the-air install link, itms-services://?action=download-manifest&url=<its manifest.plist>; null otherwise (open url). */
            install_url: string | null;
            /**
             * @description iOS: APP_STORE for a link (the App Store, TestFlight or another page), OTA for an uploaded .ipa (an enterprise or Ad Hoc signed app, installed with install_url); null on Android.
             * @enum {string|null}
             */
            ios_install: "APP_STORE" | "OTA" | null;
            /**
             * @description The Android package name or the iOS bundle identifier of an uploaded file.
             * @example vip.astras.app
             */
            package: string | null;
            /**
             * @description versionName (Android) or CFBundleShortVersionString (iOS) of an uploaded file.
             * @example 1.2.0
             */
            version: string | null;
            /**
             * @description versionCode (Android) or CFBundleVersion (iOS) of an uploaded file.
             * @example 42
             */
            build: string | null;
            /**
             * @description The lowest system it installs on (Android API level, iOS version) when the package says.
             * @example 24
             * @example 15.0
             */
            min_os: string | null;
            /**
             * Format: int64
             * @description An uploaded file's size in bytes.
             */
            size: number | null;
            /** @description An uploaded file's SHA-256, lowercase hex. */
            sha256: string | null;
            /** @description iOS only, optional - a configuration profile (.mobileconfig) uploaded beside the app, downloaded on its own. */
            mobileconfig_url: string | null;
            /** @description The version notes; empty for none. */
            notes: components["schemas"]["Texts"];
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
    getPlatformApps: {
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
            /** @description The apps. */
            200: {
                headers: {
                    ETag?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformApps"];
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
    getPlatformProducts: {
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
            /** @description The product lines. */
            200: {
                headers: {
                    ETag?: string;
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["PlatformProducts"];
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
}
