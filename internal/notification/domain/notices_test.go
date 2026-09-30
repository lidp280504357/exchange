package domain

import (
	"strings"
	"testing"
	"time"
)

func TestRenderNotice(t *testing.T) {
	at := time.Date(2026, 9, 28, 4, 5, 6, 0, time.UTC)
	sg, _ := time.LoadLocation("Asia/Singapore")
	for _, typ := range []string{
		NoticeWelcome, NoticeNewDeviceLogin, NoticeIdentityChanged, NoticePasswordChanged, NoticeAccountLocked, NoticeStatusChanged, NoticeTOTPChanged, NoticeDepositCredited, NoticeDepositUnclaimed,
		NoticeWithdrawalRequested, NoticeWithdrawalCompleted, NoticeWithdrawalRejected, NoticeWithdrawalCanceled, NoticeWithdrawalFailed,
	} {
		for _, lang := range []string{"zh-CN", "en"} {
			title, body := RenderNotice(NoticeInput{Type: typ, Language: lang, At: at, Location: sg, Data: map[string]string{
				"ip": "203.0.113.*", "channel": "EMAIL", "new": "a***@example.com", "to": "FROZEN",
				"asset": "ETH", "amount": "0.002", "network": "ETH-SEPOLIA", "tx": "0xab…cd", "reason": "BELOW_MINIMUM",
				"address": "0xfB69…d359", "fee": "0.0002", "id": "w1",
			}})
			if title == "" || body == "" || strings.Contains(body, "%!") {
				t.Errorf("%s/%s: %q %q", typ, lang, title, body)
			}
		}
	}
	_, body := RenderNotice(NoticeInput{Type: NoticeStatusChanged, Language: "en", At: at, Location: sg, Data: map[string]string{"to": "FROZEN"}})
	if !strings.Contains(body, "frozen") || !strings.Contains(body, "12:05:06 +08") {
		t.Fatalf("status body: %s", body)
	}
	_, body = RenderNotice(NoticeInput{Type: NoticeIdentityChanged, Language: "zh-CN", At: at, Data: map[string]string{
		"channel": "SMS", "old": "+65****4567", "new": "+65****7654",
	}})
	if !strings.Contains(body, "手机号") || !strings.Contains(body, "+65****7654") || !strings.Contains(body, "UTC") {
		t.Fatalf("rebind body: %s", body)
	}
}

func TestDepositNotices(t *testing.T) {
	at := time.Date(2026, 9, 29, 4, 5, 6, 0, time.UTC)
	_, body := RenderNotice(NoticeInput{Type: NoticeDepositUnclaimed, Language: "zh-CN", At: at, Data: map[string]string{
		"network": "ETH-SEPOLIA", "tx": "0xab…cd", "reason": "UNSUPPORTED_TOKEN", "amount": "",
	}})
	if !strings.Contains(body, "该代币不受支持") || strings.Contains(body, "  ") {
		t.Fatalf("unsupported token body: %s", body)
	}
	title, body := RenderNotice(NoticeInput{Type: NoticeDepositCredited, Language: "en", At: at, Data: map[string]string{
		"asset": "ETH", "amount": "0.002", "network": "ETH-SEPOLIA", "tx": "0xab…cd",
	}})
	if title != "Deposit credited" || !strings.Contains(body, "0.002 ETH") {
		t.Fatalf("credited: %q %s", title, body)
	}
}

func TestNoticeMail(t *testing.T) {
	m := NoticeMail(ChannelEmail, "a@example.com", "密码已修改", "正文", "Blue42", "zh-CN")
	if m.Subject != "【Astras】密码已修改" || !strings.HasPrefix(m.Text, "防钓鱼码：Blue42") {
		t.Fatalf("mail: %+v", m)
	}
	if m := NoticeMail(ChannelEmail, "a@example.com", "Title", "Body", "", "en"); strings.Contains(m.Text, "Anti-phishing") {
		t.Fatalf("no code, no line: %+v", m)
	}
	if m := NoticeMail(ChannelSMS, "+6591234567", "Password changed", "long body", "Blue42", "en"); m.Text != "[Astras] Password changed" {
		t.Fatalf("sms: %+v", m)
	}
}
