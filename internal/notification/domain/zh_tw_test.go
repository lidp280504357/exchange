package domain

import (
	"strings"
	"testing"
	"time"
)

// simplifiedOnly are characters of the messages that Traditional Chinese
// writes otherwise: none may reach a Traditional reader.
const simplifiedOnly = "们这说时间应设为发简体资产币种账户邮箱验证码录备请务现钱与额会过后风险线强还划转冻结销网络动态册绑审认"

func TestTraditionalReaders(t *testing.T) {
	for lang, want := range map[string]bool{
		"zh-TW": true, "zh-HK": true, "zh-MO": true, "zh-Hant": true, "zh-hant-hk": true, "zh-TW-x": true,
		"zh-CN": false, "zh": false, "zh-Hans-HK": false, "zh-SG": false, "en": false, "": false, "zh-TWN": false,
	} {
		if got := traditional(lang); got != want {
			t.Errorf("traditional(%q) = %v", lang, got)
		}
	}
}

func TestMessagesInTraditionalChinese(t *testing.T) {
	at := time.Date(2026, 10, 6, 4, 5, 6, 0, time.UTC)
	data := map[string]string{
		"ip": "203.0.113.*", "channel": "EMAIL", "new": "a***@example.com", "old": "b***@example.com", "to": "CLOSED",
		"asset": "ETH", "amount": "0.002", "network": "ETH-SEPOLIA", "tx": "0xab…cd", "reason": "ACCOUNT_CLOSED",
		"address": "0xfB69…d359", "fee": "0.0002", "id": "w1", "account_type": "MARGIN_ISOLATED", "symbol": "BTC-USDT",
		"margin_level": "1.08", "warn_level": "1.1", "liquidation_level": "1.05", "insurance_covered": "0",
	}
	for _, typ := range []string{
		NoticeWelcome, NoticeNewDeviceLogin, NoticeIdentityChanged, NoticePasswordChanged, NoticeAccountLocked, NoticeStatusChanged, NoticeTOTPChanged, NoticeDepositCredited, NoticeDepositUnclaimed,
		NoticeWithdrawalRequested, NoticeWithdrawalCompleted, NoticeWithdrawalRejected, NoticeWithdrawalCanceled, NoticeWithdrawalFailed,
		NoticeMarginWarned, NoticeMarginLiquidating, NoticeMarginLiquidated,
	} {
		for _, lang := range []string{"zh-TW", "zh-HK"} {
			title, body := RenderNotice(NoticeInput{Type: typ, Language: lang, At: at, Data: data})
			if title == "" || body == "" || strings.ContainsAny(title+body, simplifiedOnly) || strings.Contains(body, "%!") {
				t.Errorf("%s/%s: %q %q", typ, lang, title, body)
			}
			mail := NoticeMail(ChannelEmail, "a@example.com", title, body, "abcd", lang, "")
			if strings.ContainsAny(mail.Subject+mail.Text, simplifiedOnly) || !strings.Contains(mail.Text, "防釣魚碼：abcd") {
				t.Errorf("%s/%s mail: %q %q", typ, lang, mail.Subject, mail.Text)
			}
		}
	}
	title, body := RenderNotice(NoticeInput{Type: NoticeMarginWarned, Language: "zh-TW", At: at, Data: data})
	if title != "槓桿帳戶風險率預警" || !strings.Contains(body, "BTC/USDT 逐倉槓桿帳戶風險率") {
		t.Fatalf("warned: %q %s", title, body)
	}
	_, body = RenderNotice(NoticeInput{Type: NoticeIdentityChanged, Language: "zh-TW", At: at, Data: data})
	if !strings.Contains(body, "電子郵件") {
		t.Fatalf("identity: %s", body)
	}
	// Simplified readers keep the Simplified text.
	title, _ = RenderNotice(NoticeInput{Type: NoticeMarginWarned, Language: "zh-CN", At: at, Data: data})
	if title != "杠杆账户风险率预警" {
		t.Fatalf("zh-CN: %q", title)
	}
	for _, scene := range []string{"REGISTER", "LOGIN", "WITHDRAW_CONFIRM", "UNKNOWN"} {
		for _, ch := range []Channel{ChannelEmail, ChannelSMS} {
			m := OTPMessage(ch, "a@example.com", "123456", scene, "zh-TW", 5, "")
			if strings.ContainsAny(m.Subject+m.Text, simplifiedOnly) || !strings.Contains(m.Text, "123456") || !strings.Contains(m.Text, "分鐘") {
				t.Errorf("otp %s/%s: %q %q", scene, ch, m.Subject, m.Text)
			}
		}
	}
	if m := OTPMessage(ChannelEmail, "a@example.com", "123456", "REGISTER", "zh-TW", 5, "Astras"); !strings.Contains(m.Text, "用於註冊") {
		t.Fatalf("otp scene: %s", m.Text)
	}
}

func TestBroadcastInTraditionalChinese(t *testing.T) {
	b := Broadcast{
		Audience: AudienceAll,
		Title:    map[string]string{LocaleZH: "维护通知", LocaleTW: "維護通知", LocaleEN: "Maintenance"},
		Body:     map[string]string{LocaleZH: "今晚维护", LocaleTW: "今晚維護", LocaleEN: "Tonight"},
	}
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	for lang, want := range map[string]string{"zh-TW": "維護通知", "zh-HK": "維護通知", "zh-CN": "维护通知", "en-US": "Maintenance", "ja": "维护通知"} {
		if title, _ := b.In(lang); title != want {
			t.Errorf("%s: %q", lang, title)
		}
	}
	// A Traditional text left out, or half written, falls back to the Simplified.
	b.Body[LocaleTW] = " "
	if title, body := b.In("zh-TW"); title != "维护通知" || body != "今晚维护" {
		t.Fatalf("half written: %q %q", title, body)
	}
	b.Title["fr"] = "x"
	if err := b.Validate(); err == nil {
		t.Fatal("an unknown locale passed")
	}
}

func TestArticleTextsInTraditionalChinese(t *testing.T) {
	a := Article{Section: SectionHelp, Slug: "faq", Modes: ModeBoth, Texts: []ArticleText{
		{Locale: LocaleZH, Title: "常见问题", Body: "正文"},
		{Locale: LocaleTW, Title: "常見問題", Body: "正文"},
	}}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if tw, ok := a.Text(LocaleTW); !ok || tw.Title != "常見問題" {
		t.Fatalf("zh-TW: %+v %v", tw, ok)
	}
	if en, ok := a.Text(LocaleEN); ok || en.Title != "常见问题" {
		t.Fatalf("en falls back to zh-CN: %+v %v", en, ok)
	}
	a.Texts = a.Texts[1:]
	if err := a.Validate(); err == nil {
		t.Fatal("an article without its Simplified text passed")
	}
}
