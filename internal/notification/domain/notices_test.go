package domain

import (
	"strings"
	"testing"
	"time"
)

func TestRenderNotice(t *testing.T) {
	at := time.Date(2026, 9, 28, 4, 5, 6, 0, time.UTC)
	sg, _ := time.LoadLocation("Asia/Singapore")
	for _, typ := range []string{NoticeWelcome, NoticeNewDeviceLogin, NoticeIdentityChanged, NoticePasswordChanged, NoticeAccountLocked, NoticeStatusChanged} {
		for _, lang := range []string{"zh-CN", "en"} {
			title, body := RenderNotice(NoticeInput{Type: typ, Language: lang, At: at, Location: sg, Data: map[string]string{
				"ip": "203.0.113.*", "channel": "EMAIL", "new": "a***@example.com", "to": "FROZEN",
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

func TestNoticeMail(t *testing.T) {
	m := NoticeMail(ChannelEmail, "a@example.com", "密码已修改", "正文", "Blue42", "zh-CN")
	if m.Subject != "【Exchange】密码已修改" || !strings.HasPrefix(m.Text, "防钓鱼码：Blue42") {
		t.Fatalf("mail: %+v", m)
	}
	if m := NoticeMail(ChannelEmail, "a@example.com", "Title", "Body", "", "en"); strings.Contains(m.Text, "Anti-phishing") {
		t.Fatalf("no code, no line: %+v", m)
	}
	if m := NoticeMail(ChannelSMS, "+6591234567", "Password changed", "long body", "Blue42", "en"); m.Text != "[Exchange] Password changed" {
		t.Fatalf("sms: %+v", m)
	}
}
