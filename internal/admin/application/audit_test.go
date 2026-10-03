package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lidp280504357/exchange/internal/admin/domain"
	"github.com/lidp280504357/exchange/internal/admin/ports"
)

// pagedAudit serves n entries, newest first, in pages of the query's limit.
type pagedAudit struct {
	n       int
	queries []ports.AuditQuery
}

func (a *pagedAudit) Count(context.Context, ports.AuditQuery) (int, error) { return a.n, nil }

func (a *pagedAudit) Search(_ context.Context, q ports.AuditQuery) ([]ports.AuditEntry, string, error) {
	a.queries = append(a.queries, q)
	from, _ := strconv.Atoi(q.Cursor)
	out := []ports.AuditEntry{}
	for i := from; i < a.n && len(out) < q.Limit; i++ {
		payload := fmt.Sprintf(`{"action":"admin.users.note_added","reason":"note %d","details":"{}"}`, i)
		if i%2 == 1 {
			payload = `{"key":"wallet.withdraw","oldValue":"false","newValue":"true"}`
		}
		out = append(out, ports.AuditEntry{
			EventID: fmt.Sprint(i), EventType: "audit.v1.AdminActionPerformed", Actor: "boss@example.com", Target: "user:1",
			OccurredAt: time.Unix(int64(1_000_000-i), 0), Payload: json.RawMessage(payload),
		})
	}
	next := ""
	if from+len(out) < a.n {
		next = strconv.Itoa(from + len(out))
	}
	return out, next, nil
}

func TestExportingTheAuditTrail(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.admin(t, "boss@example.com", domain.RoleAdmin)
	h.admin(t, "fin@example.com", domain.RoleFinance)
	boss, fin := h.login(t, "boss@example.com"), h.login(t, "fin@example.com")
	src := &pagedAudit{n: 1203}
	h.svc.AuditLog = src

	h.admin(t, "audit@example.com", domain.RoleAuditor)
	auditor := h.login(t, "audit@example.com")
	export := func(p Principal, q ports.AuditQuery) ([]AuditRow, bool, error) {
		var rows []AuditRow
		var cut bool
		err := h.svc.ExportAuditLogs(ctx, p, q, func(c bool) error {
			cut = c
			return nil
		}, func(r AuditRow) error {
			rows = append(rows, r)
			return nil
		})
		return rows, cut, err
	}
	// Email and IP addresses: only ADMIN and AUDITOR export (C5.5 ⑪).
	if _, _, err := export(fin, ports.AuditQuery{}); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("FINANCE exports: %v", err)
	}
	rows, cut, err := export(auditor, ports.AuditQuery{Actor: "boss@example.com", Cursor: "77", Limit: 3})
	if err != nil || cut || len(rows) != 1203 || len(src.queries) != 3 {
		t.Fatalf("%d rows, cut %v, %d pages: %v", len(rows), cut, len(src.queries), err)
	}
	if q := src.queries[0]; q.Cursor != "" || q.Limit != 500 || q.Actor != "boss@example.com" {
		t.Fatalf("the first page starts at the top: %+v", q)
	}
	if r := rows[0]; r.Action != "admin.users.note_added" || r.Reason != "note 0" || r.Details != "{}" {
		t.Fatalf("an administrator action %+v", r)
	}
	if r := rows[1]; r.Action != "" || r.Details != `{"new":"true","old":"false"}` {
		t.Fatalf("a configuration change %+v", r)
	}
	last := h.store.audits[len(h.store.audits)-1]
	if last.GetAction() != "admin.audit.exported" || last.GetActor() != "audit@example.com" ||
		!strings.Contains(last.GetDetails(), `"rows":1203`) || !strings.Contains(last.GetDetails(), `"actor":"boss@example.com"`) {
		t.Fatalf("the export is audited: %v", last)
	}

	src.n, src.queries = MaxAuditExport+1, nil
	rows, cut, err = export(boss, ports.AuditQuery{})
	if err != nil || !cut || len(rows) != MaxAuditExport {
		t.Fatalf("over the bound: %d rows, cut %v: %v", len(rows), cut, err)
	}

	h.admin(t, "ops@example.com", domain.RoleOperator)
	if _, _, err := export(h.login(t, "ops@example.com"), ports.AuditQuery{}); code(err) != "ADMIN_FORBIDDEN" {
		t.Fatalf("an OPERATOR exports: %v", err)
	}
}
