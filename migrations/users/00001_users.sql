-- user-service: profiles, account status and consents (requirements §5.4,
-- §12.5). Identities live in auth-service; this schema holds no contact
-- data.

-- +goose Up
CREATE TABLE users (
    id                 uuid        PRIMARY KEY,
    status             text        NOT NULL DEFAULT 'ACTIVE'
                                   CHECK (status IN ('ACTIVE', 'RISK_REVIEW', 'FROZEN', 'CLOSED')),
    -- ISO 3166-1 alpha-2 region used for eligibility and flags.
    region             text        NOT NULL,
    language           text        NOT NULL DEFAULT 'zh-CN',
    timezone           text        NOT NULL DEFAULT 'Asia/Shanghai',
    -- Shown in every mail we send, so phishing mails can be told apart.
    anti_phishing_code text        NOT NULL DEFAULT '',
    notify_prefs       jsonb       NOT NULL DEFAULT '{}',
    -- Reserved for KYC levels (§12.5); 0 means none.
    kyc_level          int         NOT NULL DEFAULT 0,
    version            bigint      NOT NULL DEFAULT 1,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);

-- Every status change with its reason code and actor (§5.4).
CREATE TABLE user_status_changes (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     uuid        NOT NULL REFERENCES users (id),
    from_status text        NOT NULL,
    to_status   text        NOT NULL,
    reason_code text        NOT NULL,
    actor       text        NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_status_changes_user ON user_status_changes (user_id, id);

-- Accepted terms of service and risk disclosures, by version (§12.5).
CREATE TABLE consents (
    user_id     uuid        NOT NULL REFERENCES users (id),
    document    text        NOT NULL CHECK (document IN ('TERMS', 'RISK_DISCLOSURE')),
    version     text        NOT NULL,
    accepted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, document, version)
);

-- +goose Down
DROP TABLE consents;
DROP TABLE user_status_changes;
DROP TABLE users;
