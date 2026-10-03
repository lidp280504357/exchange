-- One live request per deposit of nobody: while a request to credit it
-- waits (perhaps attempted) or once one was carried out, another is
-- refused, so two operations never share its release and its worth is
-- counted once against the single-person limits (review ㉕).

-- +goose Up
CREATE UNIQUE INDEX approvals_deposit_assign_live ON approvals ((payload ->> 'deposit_id'))
    WHERE kind = 'DEPOSIT_ASSIGN' AND status IN ('PENDING', 'EXECUTED');

-- +goose Down
DROP INDEX approvals_deposit_assign_live;
