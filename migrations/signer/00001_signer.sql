-- signer (requirements §5.10, ADR-0003; plan §6.3 task 10): the audit of
-- every signature and every refusal, append-only (enforced by triggers).
-- The signer's own limits are computed from it. Values are integers in
-- the chain's smallest unit.

-- +goose Up
CREATE TABLE signatures (
    request_id   text           PRIMARY KEY,
    request_hash bytea          NOT NULL,
    purpose      text           NOT NULL CHECK (purpose IN ('WITHDRAWAL', 'SWEEP')),
    reference    text           NOT NULL,
    approved_by  text           NOT NULL DEFAULT '',
    chain_id     bigint         NOT NULL,
    from_address text           NOT NULL,
    to_address   text           NOT NULL,
    value        numeric(78,0)  NOT NULL CHECK (value >= 0),
    nonce        bigint         NOT NULL CHECK (nonce >= 0),
    gas_limit    bigint         NOT NULL CHECK (gas_limit > 0),
    max_fee      numeric(78,0)  NOT NULL,
    max_tip      numeric(78,0)  NOT NULL,
    tx_hash      text           NOT NULL,
    raw_tx       text           NOT NULL,
    created_at   timestamptz    NOT NULL DEFAULT now()
);
CREATE INDEX signatures_reference_idx ON signatures (reference);
CREATE INDEX signatures_purpose_idx ON signatures (purpose, created_at);

CREATE TABLE refusals (
    id         bigserial   PRIMARY KEY,
    request_id text        NOT NULL,
    purpose    text        NOT NULL,
    reference  text        NOT NULL,
    reason     text        NOT NULL,
    request    jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose StatementBegin
CREATE FUNCTION signer_append_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'the signer audit is append-only';
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER signatures_append_only BEFORE UPDATE OR DELETE ON signatures
    FOR EACH ROW EXECUTE FUNCTION signer_append_only();
CREATE TRIGGER refusals_append_only BEFORE UPDATE OR DELETE ON refusals
    FOR EACH ROW EXECUTE FUNCTION signer_append_only();
CREATE TRIGGER signatures_no_truncate BEFORE TRUNCATE ON signatures
    FOR EACH STATEMENT EXECUTE FUNCTION signer_append_only();
CREATE TRIGGER refusals_no_truncate BEFORE TRUNCATE ON refusals
    FOR EACH STATEMENT EXECUTE FUNCTION signer_append_only();

-- +goose Down
DROP TRIGGER refusals_no_truncate ON refusals;
DROP TRIGGER signatures_no_truncate ON signatures;
DROP TRIGGER refusals_append_only ON refusals;
DROP TRIGGER signatures_append_only ON signatures;
DROP FUNCTION signer_append_only();
DROP TABLE refusals;
DROP TABLE signatures;
