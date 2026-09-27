-- Tables every service schema gets from the platform (applied before the
-- service's own migrations, tracked in platform_db_version).

-- +goose Up

-- Transactional outbox (requirements §8.2): rows are written in the same
-- transaction as the business change and published by the relay in id
-- order, so events of one partition key keep their order.
CREATE TABLE outbox (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id      uuid        NOT NULL UNIQUE,
    topic         text        NOT NULL,
    partition_key text        NOT NULL,
    event_type    text        NOT NULL,
    envelope      bytea       NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    published_at  timestamptz
);
CREATE INDEX outbox_pending ON outbox (id) WHERE published_at IS NULL;
CREATE INDEX outbox_published_at ON outbox (published_at) WHERE published_at IS NOT NULL;

-- Inbox: one row per event a consumer has handled, written in the
-- handler's transaction so that a redelivered event is skipped.
CREATE TABLE inbox (
    consumer    text        NOT NULL,
    event_id    uuid        NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
CREATE INDEX inbox_received_at ON inbox (received_at);

-- Idempotency keys of write requests (requirements §7.1). The scope is
-- user + method + path; a key is kept 24 hours; the stored response is
-- replayed for a repeated request with the same body.
CREATE TABLE idempotency_keys (
    scope        text        NOT NULL,
    key          text        NOT NULL,
    request_hash bytea       NOT NULL,
    status_code  int,
    response     bytea,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, key)
);
CREATE INDEX idempotency_keys_created_at ON idempotency_keys (created_at);

-- +goose Down
DROP TABLE idempotency_keys;
DROP TABLE inbox;
DROP TABLE outbox;
