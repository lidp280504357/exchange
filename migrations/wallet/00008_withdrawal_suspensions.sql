-- An asset's withdrawals suspended (design 2026-09-30 §9, review B4): the
-- custody check found funds missing that no withdrawal with an unknown
-- outcome explains, on two checks minutes apart, or an operator suspended
-- them. New withdrawals of the asset are refused and approved ones wait
-- until a person lifts it: the row goes, the audit keeps the history.

-- +goose Up
CREATE TABLE withdrawal_suspensions (
    asset        text           PRIMARY KEY,
    shortfall    numeric(38,18) NOT NULL DEFAULT 0 CHECK (shortfall >= 0),
    reason       text           NOT NULL,
    suspended_by text           NOT NULL,
    suspended_at timestamptz    NOT NULL
);

-- +goose Down
DROP TABLE withdrawal_suspensions;
