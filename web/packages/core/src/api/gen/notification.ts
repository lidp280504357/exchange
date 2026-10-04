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
    "/v1/announcements": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The published announcements, pinned first then newest
         * @description Written and published in the admin console (design 2026-10-02
         *     §4.5); a scheduled one shows from its time. Public. Cached for 15
         *     seconds: the sites show a new one within a minute. A page at a
         *     time, summaries only (C5.5 ⑫).
         */
        get: operations["listAnnouncements"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/announcements/{slug}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A published announcement */
        get: operations["getAnnouncement"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/help": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The published help articles, by category and order
         * @description A page at a time, summaries only (C5.5 ⑫).
         */
        get: operations["listHelpArticles"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/help/{slug}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A published help article */
        get: operations["getHelpArticle"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/legal": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The published legal and information pages
         * @description The console's versions of the fixed pages (design 2026-10-04 §4.4):
         *     terms, privacy, risk, fees, about, contact. A page the console did
         *     not publish shows the sites' bundled draft; a withdrawn one shows
         *     nothing.
         */
        get: operations["listLegalPages"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/legal/{slug}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A published legal or information page */
        get: operations["getLegalPage"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/home": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /**
         * The published blocks of the home page
         * @description Today one, home-hero: the title is the hero's title, the summary its
         *     subtitle, and the body's first Markdown link its button
         *     ([text](/path or https URL)).
         */
        get: operations["listHomeBlocks"];
        put?: never;
        post?: never;
        delete?: never;
        options?: never;
        head?: never;
        patch?: never;
        trace?: never;
    };
    "/v1/home/{slug}": {
        parameters: {
            query?: never;
            header?: never;
            path?: never;
            cookie?: never;
        };
        /** A published block of the home page */
        get: operations["getHomeBlock"];
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
        /**
         * @description The fixed legal and information pages (design 2026-10-04 §4.4).
         * @enum {string}
         */
        LegalSlug: "terms" | "privacy" | "risk" | "fees" | "about" | "contact";
        /**
         * @description The home page's blocks (design 2026-10-04 §4.4).
         * @enum {string}
         */
        HomeSlug: "home-hero";
        ArticleSummary: {
            slug: string;
            /** @description Announcements notice, product or another; help account, funds, trading, futures, faq or another. */
            category: string;
            pinned: boolean;
            /** @description Sorts help articles within their category. */
            order: number;
            title: string;
            summary: string;
            /** Format: date-time */
            published_at: string;
            /** @enum {string} */
            locale: "zh-CN" | "en";
            /** @description No text in the language asked; the Chinese one instead. */
            fallback: boolean;
            /** @description Grows with every change; the sites may cache by it. */
            version: number;
        };
        ArticleList: {
            /** @description A page of summaries; a list never carries the bodies. */
            items: components["schemas"]["ArticleSummary"][];
            /** @description The next page's cursor; null after the last. */
            next_cursor: string | null;
            /** @description Slugs of articles of the exchange's mode taken off; the sites hide their bundled file of such a slug too. */
            withdrawn: string[];
        };
        Article: components["schemas"]["ArticleSummary"] & {
            /** @description Markdown. It may hold mode blocks, ":::test" or ":::formal" up to a line ":::", which the sites keep in that mode only (design 2026-10-04 §4.4); the service sends them as written. */
            body: string;
        };
        Notification: {
            /** Format: uuid */
            id: string;
            /** @enum {string} */
            type: "WELCOME" | "NEW_DEVICE_LOGIN" | "IDENTITY_CHANGED" | "PASSWORD_CHANGED" | "ACCOUNT_LOCKED" | "STATUS_CHANGED" | "TOTP_CHANGED" | "DEPOSIT_CREDITED" | "DEPOSIT_UNCLAIMED" | "WITHDRAWAL_REQUESTED" | "WITHDRAWAL_COMPLETED" | "WITHDRAWAL_REJECTED" | "WITHDRAWAL_CANCELED" | "WITHDRAWAL_FAILED" | "BROADCAST";
            title: string;
            /** @description Rendered in the user's language and time zone. */
            body: string;
            /** @description Masked specifics, e.g. ip, channel, to; a BROADCAST (an operator's message) carries broadcast_id and maybe link, a path on the sites. */
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
        /**
         * @description COMMON_NOT_FOUND: no such article published (the sites show their
         *     bundled file of the slug, if any); NOTIFY_ARTICLE_WITHDRAWN: taken
         *     off (they do not).
         */
        ArticleNotFound: {
            headers: {
                [name: string]: unknown;
            };
            content: {
                "application/json": components["schemas"]["Error"];
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
        /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
        Locale: "zh-CN" | "en";
        Slug: string;
        /** @description Articles on a page (20 when absent, at most 100). */
        ListLimit: number;
        /** @description The previous page's next_cursor; COMMON_INVALID_ARGUMENT when it is not one. */
        ListCursor: string;
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
    listAnnouncements: {
        parameters: {
            query?: {
                /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
                locale?: components["parameters"]["Locale"];
                /** @description Articles on a page (20 when absent, at most 100). */
                limit?: components["parameters"]["ListLimit"];
                /** @description The previous page's next_cursor; COMMON_INVALID_ARGUMENT when it is not one. */
                cursor?: components["parameters"]["ListCursor"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The announcements. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ArticleList"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getAnnouncement: {
        parameters: {
            query?: {
                /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
                locale?: components["parameters"]["Locale"];
            };
            header?: never;
            path: {
                slug: components["parameters"]["Slug"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The announcement with its Markdown body. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Article"];
                };
            };
            404: components["responses"]["ArticleNotFound"];
            default: components["responses"]["Error"];
        };
    };
    listHelpArticles: {
        parameters: {
            query?: {
                /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
                locale?: components["parameters"]["Locale"];
                /** @description Articles on a page (20 when absent, at most 100). */
                limit?: components["parameters"]["ListLimit"];
                /** @description The previous page's next_cursor; COMMON_INVALID_ARGUMENT when it is not one. */
                cursor?: components["parameters"]["ListCursor"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The help articles. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ArticleList"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getHelpArticle: {
        parameters: {
            query?: {
                /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
                locale?: components["parameters"]["Locale"];
            };
            header?: never;
            path: {
                slug: components["parameters"]["Slug"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The article with its Markdown body. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Article"];
                };
            };
            404: components["responses"]["ArticleNotFound"];
            default: components["responses"]["Error"];
        };
    };
    listLegalPages: {
        parameters: {
            query?: {
                /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
                locale?: components["parameters"]["Locale"];
                /** @description Articles on a page (20 when absent, at most 100). */
                limit?: components["parameters"]["ListLimit"];
                /** @description The previous page's next_cursor; COMMON_INVALID_ARGUMENT when it is not one. */
                cursor?: components["parameters"]["ListCursor"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The pages. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ArticleList"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getLegalPage: {
        parameters: {
            query?: {
                /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
                locale?: components["parameters"]["Locale"];
            };
            header?: never;
            path: {
                slug: components["schemas"]["LegalSlug"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The page with its Markdown body. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Article"];
                };
            };
            404: components["responses"]["ArticleNotFound"];
            default: components["responses"]["Error"];
        };
    };
    listHomeBlocks: {
        parameters: {
            query?: {
                /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
                locale?: components["parameters"]["Locale"];
                /** @description Articles on a page (20 when absent, at most 100). */
                limit?: components["parameters"]["ListLimit"];
                /** @description The previous page's next_cursor; COMMON_INVALID_ARGUMENT when it is not one. */
                cursor?: components["parameters"]["ListCursor"];
            };
            header?: never;
            path?: never;
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The blocks. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["ArticleList"];
                };
            };
            default: components["responses"]["Error"];
        };
    };
    getHomeBlock: {
        parameters: {
            query?: {
                /** @description en for English; Chinese otherwise. An article without English comes in Chinese (fallback true). */
                locale?: components["parameters"]["Locale"];
            };
            header?: never;
            path: {
                slug: components["schemas"]["HomeSlug"];
            };
            cookie?: never;
        };
        requestBody?: never;
        responses: {
            /** @description The block with its Markdown body. */
            200: {
                headers: {
                    [name: string]: unknown;
                };
                content: {
                    "application/json": components["schemas"]["Article"];
                };
            };
            404: components["responses"]["ArticleNotFound"];
            default: components["responses"]["Error"];
        };
    };
}
