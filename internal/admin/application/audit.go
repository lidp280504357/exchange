package application

import (
	"context"
	"encoding/json"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
)

// MaxAuditExport bounds an export of the audit trail; narrow the filters
// beyond it.
const MaxAuditExport = 10_000

// AuditRow is an audit entry flattened for a spreadsheet: what the
// payload says of the action, besides the entry itself.
type AuditRow struct {
	ports.AuditEntry
	Action  string
	Reason  string
	Details string
}

// ExportAuditLogs returns the audit entries matching q, newest first, at
// most MaxAuditExport of them, and whether more were left out; the
// export itself is audited (admin.audit.exported) with its filters.
func (s *Service) ExportAuditLogs(ctx context.Context, p Principal, q ports.AuditQuery) ([]AuditRow, bool, error) {
	if err := p.require(domain.PermAuditRead); err != nil {
		return nil, false, err
	}
	q.Cursor, q.Limit = "", 500
	var out []AuditRow
	for {
		page, next, err := s.AuditLog.Search(ctx, q)
		if err != nil {
			return nil, false, err
		}
		for _, e := range page {
			if len(out) == MaxAuditExport {
				return out, true, s.auditExport(ctx, p, q, len(out))
			}
			out = append(out, auditRow(e))
		}
		if next == "" {
			return out, false, s.auditExport(ctx, p, q, len(out))
		}
		q.Cursor = next
	}
}

func (s *Service) auditExport(ctx context.Context, p Principal, q ports.AuditQuery, rows int) error {
	details, _ := json.Marshal(map[string]any{
		"actor": q.Actor, "target": q.Target, "event_type": q.EventType, "from": q.From, "to": q.To, "rows": rows,
	})
	return s.audit(ctx, p, "audit", "admin.audit.exported", "export", string(details))
}

// auditRow reads the action, reason and details of the console's and the
// services' audit events (AdminActionPerformed, ConfigChanged).
func auditRow(e ports.AuditEntry) AuditRow {
	var payload struct {
		Action   string `json:"action"`
		Reason   string `json:"reason"`
		Details  string `json:"details"`
		OldValue string `json:"oldValue"`
		NewValue string `json:"newValue"`
	}
	_ = json.Unmarshal(e.Payload, &payload)
	row := AuditRow{AuditEntry: e, Action: payload.Action, Reason: payload.Reason, Details: payload.Details}
	if row.Details == "" && (payload.OldValue != "" || payload.NewValue != "") {
		b, _ := json.Marshal(map[string]string{"old": payload.OldValue, "new": payload.NewValue})
		row.Details = string(b)
	}
	return row
}
