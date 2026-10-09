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
		NoticeMarginWarned, NoticeMarginLiquidating, NoticeMarginLiquidated, NoticeContractWarned, NoticeContractLiquidating, NoticeContractDeleveraged,
		NoticeContractLiquidated, NoticeUsernameReset, NoticeAvatarReset,
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
	// The authenticator removed: withdrawals are reviewed for a day after
	// it (C5.5 ⑤), and the mail says so (review ⑭); binding one does not.
	for lang, want := range map[string]string{"zh-CN": "24 小时内提现需人工审核", "en": "for 24 hours after it, withdrawals are reviewed"} {
		_, body = RenderNotice(NoticeInput{Type: NoticeTOTPChanged, Language: lang, At: at, Data: map[string]string{"enabled": "false"}})
		if !strings.Contains(body, want) {
			t.Fatalf("%s removed: %s", lang, body)
		}
		_, body = RenderNotice(NoticeInput{Type: NoticeTOTPChanged, Language: lang, At: at, Data: map[string]string{"enabled": "true"}})
		if strings.Contains(body, "24") {
			t.Fatalf("%s bound: %s", lang, body)
		}
	}
}

// TestProfileResetNotices: an operator's reset names the new username
// (B140); neither tells the operator's reason.
func TestProfileResetNotices(t *testing.T) {
	at := time.Date(2026, 10, 7, 4, 5, 6, 0, time.UTC)
	for _, lang := range []string{"zh-CN", "zh-TW", "en"} {
		title, body := RenderNotice(NoticeInput{Type: NoticeUsernameReset, Language: lang, At: at, Data: map[string]string{"username": "user_k3x9q2m7"}})
		if title == "" || !strings.Contains(body, "user_k3x9q2m7") || strings.Contains(body, "%!") {
			t.Fatalf("%s username: %q %s", lang, title, body)
		}
		title, body = RenderNotice(NoticeInput{Type: NoticeAvatarReset, Language: lang, At: at})
		if title == "" || body == "" || strings.Contains(body, "%!") {
			t.Fatalf("%s avatar: %q %s", lang, title, body)
		}
	}
}

func TestMarginNotices(t *testing.T) {
	at := time.Date(2026, 10, 6, 4, 5, 6, 0, time.UTC)
	title, body := RenderNotice(NoticeInput{Type: NoticeMarginWarned, Language: "zh-CN", At: at, Data: map[string]string{
		"account_type": "MARGIN_ISOLATED", "symbol": "BTC-USDT", "margin_level": "1.08", "warn_level": "1.1", "liquidation_level": "1.05",
	}})
	if title != "杠杆账户风险率预警" || !strings.Contains(body, "BTC/USDT 逐仓杠杆账户风险率") || !strings.Contains(body, "强平线 1.05") {
		t.Fatalf("warned: %q %s", title, body)
	}
	_, body = RenderNotice(NoticeInput{Type: NoticeMarginLiquidated, Language: "en", At: at, Data: map[string]string{
		"account_type": "MARGIN_CROSS", "repaid": "100 USDT, 0.01 BTC", "fee": "2.1", "insurance_covered": "0", "remaining": "",
	}})
	if !strings.Contains(body, "cross margin account") || !strings.Contains(body, "repaid 100 USDT, 0.01 BTC") || !strings.Contains(body, "account: none") {
		t.Fatalf("liquidated: %s", body)
	}
}

func TestContractNotices(t *testing.T) {
	at := time.Date(2026, 10, 7, 4, 5, 6, 0, time.UTC)
	render := func(typ, lang string, d map[string]string) (string, string) {
		return RenderNotice(NoticeInput{Type: typ, Language: lang, At: at, Data: d})
	}
	// The amounts are in the contract's settlement asset: the coin of a
	// coin-margined contract, USDT otherwise (also when an older event
	// carries none).
	title, body := render(NoticeContractWarned, "zh-CN", map[string]string{"cross": "true", "margin_balance": "0.0013", "maintenance_margin": "0.0012", "settle_asset": "BTC"})
	if title != "合约强平预警" || !strings.Contains(body, "BTC 合约全仓账户保证金余额") || !strings.Contains(body, "维持保证金 0.0012 BTC") {
		t.Fatalf("cross warning: %q %s", title, body)
	}
	_, body = render(NoticeContractWarned, "en", map[string]string{"cross": "false", "symbol": "ETH-USDT-PERP", "margin_balance": "12.5", "maintenance_margin": "11"})
	if !strings.Contains(body, "isolated ETHUSDT perpetual position fell to 12.5 USDT") || !strings.Contains(body, "11 USDT") {
		t.Fatalf("isolated warning: %s", body)
	}
	title, body = render(NoticeContractLiquidating, "zh-CN", map[string]string{"symbol": "BTC-USD-PERP", "side": "LONG", "quantity": "3", "mark_price": "85000.1", "settle_asset": "BTC"})
	if title != "合约仓位强平" || !strings.Contains(body, "BTCUSD 永续多仓（3 张）") || !strings.Contains(body, "标记价格 85000.1") || !strings.Contains(body, "以 BTC 结算") {
		t.Fatalf("liquidating: %q %s", title, body)
	}
	_, body = render(NoticeContractLiquidating, "en", map[string]string{"symbol": "ETH-USDT-PERP", "side": "SHORT", "quantity": "0.5", "mark_price": "2600"})
	if !strings.Contains(body, "ETHUSDT perpetual short position (0.5 ETH)") || !strings.Contains(body, "settled in USDT") {
		t.Fatalf("liquidating en: %s", body)
	}
	_, body = render(NoticeContractDeleveraged, "zh-CN", map[string]string{"symbol": "ETH-USD-PERP", "quantity": "7", "price": "2600.5", "realized_pnl": "0.0123", "settle_asset": "ETH"})
	if !strings.Contains(body, "ETHUSD 永续仓位") || !strings.Contains(body, "自动减仓 7 张（成交价 2600.5）") || !strings.Contains(body, "已实现盈亏 0.0123 ETH") {
		t.Fatalf("adl: %s", body)
	}
	title, _ = render(NoticeContractLiquidating, "zh-TW", map[string]string{"symbol": "BTC-USD-PERP", "side": "LONG", "quantity": "3"})
	if title != "合約倉位強平" {
		t.Fatalf("zh-TW title: %q", title)
	}
	// Review FG, B133: a cross takeover is one notice for the account; the
	// side shows where the event has one; the unit follows the face value.
	title, body = render(NoticeContractLiquidating, "zh-CN", map[string]string{"cross": "true", "symbol": "BTC-USD-PERP", "settle_asset": "BTC"})
	if title != "合约全仓账户强平" || !strings.Contains(body, "BTC 合约全仓账户已于") || !strings.Contains(body, "接管全部全仓仓位") ||
		!strings.Contains(body, "剩余保证金将作为强平清算费划入保险基金") {
		t.Fatalf("cross liquidating: %q %s", title, body)
	}
	// C68: a cross account's liquidation over, what it left gone to the
	// insurance fund, the amount named; or nothing left.
	title, body = render(NoticeContractLiquidated, "zh-CN", map[string]string{"settle_asset": "USDT", "clearance_fee": "897.98"})
	if title != "合约全仓账户强平完成" || !strings.Contains(body, "USDT 合约全仓账户强平已于") ||
		!strings.Contains(body, "剩余保证金 897.98 USDT 已作为强平清算费划入保险基金") {
		t.Fatalf("liquidated: %q %s", title, body)
	}
	_, body = render(NoticeContractLiquidated, "en", map[string]string{"settle_asset": "BTC", "clearance_fee": "0.0012"})
	if !strings.Contains(body, "the margin left, 0.0012 BTC, went to the insurance fund as the liquidation clearance fee") {
		t.Fatalf("liquidated en: %s", body)
	}
	_, body = render(NoticeContractLiquidated, "zh-CN", map[string]string{"settle_asset": "USDT", "clearance_fee": "0"})
	if !strings.Contains(body, "账户没有剩余保证金") || strings.Contains(body, "清算费") {
		t.Fatalf("liquidated, nothing left: %s", body)
	}
	title, body = render(NoticeContractLiquidated, "zh-TW", map[string]string{"settle_asset": "USDT", "clearance_fee": "1"})
	if title != "合約全倉帳戶強平完成" || !strings.Contains(body, "保險基金") {
		t.Fatalf("liquidated zh-TW: %q %s", title, body)
	}
	_, body = render(NoticeContractWarned, "zh-CN", map[string]string{"cross": "false", "symbol": "BTC-USDT-PERP", "side": "SHORT", "margin_balance": "9", "maintenance_margin": "8"})
	if !strings.Contains(body, "BTCUSDT 永续 逐仓空仓保证金余额") {
		t.Fatalf("isolated short warning: %s", body)
	}
	_, body = render(NoticeContractDeleveraged, "en", map[string]string{"symbol": "ETH-USDT-PERP", "side": "LONG", "quantity": "0.4", "contract_size": "0", "price": "2600", "realized_pnl": "3"})
	if !strings.Contains(body, "0.4 ETH of your ETHUSDT perpetual long position") {
		t.Fatalf("adl en: %s", body)
	}
	_, body = render(NoticeContractLiquidating, "en", map[string]string{"symbol": "ASTRA-USD-PERP", "side": "SHORT", "quantity": "12", "contract_size": "10", "settle_asset": "ASTRA"})
	if !strings.Contains(body, "(12 contracts)") {
		t.Fatalf("contracts by face value: %s", body)
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
	m := NoticeMail(ChannelEmail, "a@example.com", "密码已修改", "正文", "Blue42", "zh-CN", "")
	if m.Subject != "【Astras】密码已修改" || !strings.HasPrefix(m.Text, "防钓鱼码：Blue42") {
		t.Fatalf("mail: %+v", m)
	}
	if m := NoticeMail(ChannelEmail, "a@example.com", "Title", "Body", "", "en", ""); strings.Contains(m.Text, "Anti-phishing") {
		t.Fatalf("no code, no line: %+v", m)
	}
	if m := NoticeMail(ChannelSMS, "+6591234567", "Password changed", "long body", "Blue42", "en", ""); m.Text != "[Astras] Password changed" {
		t.Fatalf("sms: %+v", m)
	}
}
