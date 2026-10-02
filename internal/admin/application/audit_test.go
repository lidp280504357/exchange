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

	rows, cut, err := h.svc.ExportAuditLogs(ctx, fin, ports.AuditQuery{Actor: "boss@example.com", Cursor: "77", Limit: 3})
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
	if last.GetAction() != "admin.audit.exported" || last.GetActor() != "fin@example.com" ||
		!strings.Contains(last.GetDetails(), `"rows":1203`) || !strings.Contains(last.GetDetails(), `"actor":"boss@example.com"`) {
		t.Fatalf("the export is audited: %v", last)
	}

	src.n, src.queries = MaxAuditExport+1, nil
	rows, cut, err = h.svc.ExportAuditLogs(ctx, boss, ports.AuditQuery{})
	if err != nil || !cut || len(rows) != MaxAuditExport {
		t.Fatalf("over the bound: %d rows, cut %v: %v", len(rows), cut, err)
	}

	h.admin(t, "ops@example.com", domain.RoleOperator)
	if _, _, err := h.svc.ExportAuditLogs(ctx, h.login(t, "ops@example.com"), ports.AuditQuery{}); err != nil {
		t.Fatalf("every role reads the audit trail: %v", err)
	}
}
