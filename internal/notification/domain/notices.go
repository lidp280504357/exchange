package domain

import (
	"fmt"
	"strings"
	"time"
)

// Notice types, the "type" of NotificationCreated.
const (
	NoticeWelcome         = "WELCOME"
	NoticeNewDeviceLogin  = "NEW_DEVICE_LOGIN"
	NoticeIdentityChanged = "IDENTITY_CHANGED"
	NoticePasswordChanged = "PASSWORD_CHANGED"
	NoticeAccountLocked   = "ACCOUNT_LOCKED"
	NoticeStatusChanged   = "STATUS_CHANGED"
	NoticeTOTPChanged     = "TOTP_CHANGED"
	// Deposits (§11.5): credited to the account, or held for manual
	// handling (below the minimum, unsupported token, closed account).
	NoticeDepositCredited  = "DEPOSIT_CREDITED"
	NoticeDepositUnclaimed = "DEPOSIT_UNCLAIMED"
	// Withdrawals (§11.6): requested, completed, rejected, canceled, failed.
	NoticeWithdrawalRequested = "WITHDRAWAL_REQUESTED"
	NoticeWithdrawalCompleted = "WITHDRAWAL_COMPLETED"
	NoticeWithdrawalRejected  = "WITHDRAWAL_REJECTED"
	NoticeWithdrawalCanceled  = "WITHDRAWAL_CANCELED"
	NoticeWithdrawalFailed    = "WITHDRAWAL_FAILED"
	// Margin accounts (margin design 2026-10-06 §4.5): under the warning
	// level, a liquidation started, and its outcome.
	NoticeMarginWarned      = "MARGIN_WARNED"
	NoticeMarginLiquidating = "MARGIN_LIQUIDATING"
	NoticeMarginLiquidated  = "MARGIN_LIQUIDATED"
)

// Notice is an in-app notification.
type Notice struct {
	ID        string
	UserID    string
	Type      string
	Title     string
	Body      string
	Data      map[string]string
	CreatedAt time.Time
	ReadAt    time.Time
}

// NoticeInput is what a notice is rendered from. Data holds masked values
// only (identity masks, IP masks), never raw personal data.
type NoticeInput struct {
	Type     string
	Language string
	// At is when it happened, shown in Location.
	At       time.Time
	Location *time.Location
	Data     map[string]string
	// Brand names the exchange (DefaultBrand when empty).
	Brand string
}

var statusNames = map[string][2]string{
	"ACTIVE":      {"正常", "active"},
	"RISK_REVIEW": {"风控审核中", "under review"},
	"FROZEN":      {"已冻结", "frozen"},
	"CLOSED":      {"已注销", "closed"},
}

var depositReasons = map[string][2]string{
	"BELOW_MINIMUM":     {"金额低于该网络的最小充值额", "the amount is below the network's minimum deposit"},
	"UNSUPPORTED_TOKEN": {"该代币不受支持", "the token is not supported"},
	"ACCOUNT_CLOSED":    {"账户已注销", "the account is closed"},
	"NOT_ELIGIBLE":      {"账户当前不能充值", "the account cannot take deposits now"},
}

// marginAccount names a margin account: the cross one, or the isolated
// one of a pair (BTC-USDT shown as BTC/USDT).
func marginAccount(accountType, symbol string, en bool) string {
	pair := strings.Replace(symbol, "-", "/", 1)
	switch {
	case accountType == "MARGIN_ISOLATED" && en:
		return "isolated margin account " + pair
	case accountType == "MARGIN_ISOLATED":
		return pair + " 逐仓杠杆账户"
	case en:
		return "cross margin account"
	default:
		return "全仓杠杆账户"
	}
}

var channelNames = map[string][2]string{
	"EMAIL": {"邮箱", "email address"},
	"SMS":   {"手机号", "phone number"},
}

func pick(names map[string][2]string, key string, en bool) string {
	n, ok := names[key]
	switch {
	case !ok:
		return key
	case en:
		return n[1]
	default:
		return n[0]
	}
}

// RenderNotice returns the title and body of a notice in the user's
// language (zh-CN unless it starts with "en").
func RenderNotice(in NoticeInput) (title, body string) {
	en := english(in.Language)
	loc := in.Location
	if loc == nil {
		loc = time.UTC
	}
	when := in.At.In(loc).Format("2006-01-02 15:04:05 MST")
	d := in.Data
	switch in.Type {
	case NoticeWelcome:
		if en {
			return "Welcome to " + brandOr(in.Brand), "Your account is ready. Set an anti-phishing code in your profile: it appears in every mail we send."
		}
		return "欢迎加入 " + brandOr(in.Brand), "账户已开通。建议在个人资料中设置防钓鱼码，我们发出的每封邮件都会带上它。"
	case NoticeNewDeviceLogin:
		if en {
			return "New device sign-in", fmt.Sprintf("Your account was signed in from a new device at %s (IP %s, %s). "+
				"If this was not you, change your password and sign out other devices now.", when, d["ip"], d["user_agent"])
		}
		return "新设备登录提醒", fmt.Sprintf("您的账户于 %s 在新设备上登录（IP %s，%s）。如非本人操作，请立即修改密码并退出其他设备。",
			when, d["ip"], d["user_agent"])
	case NoticeIdentityChanged:
		ch := pick(channelNames, d["channel"], en)
		if d["old"] != "" {
			if en {
				return "Contact changed", fmt.Sprintf("The %s of your account changed from %s to %s at %s. "+
					"If this was not you, contact support now.", ch, d["old"], d["new"], when)
			}
			return "身份信息已更换", fmt.Sprintf("您账户的%s已于 %s 从 %s 更换为 %s。如非本人操作，请立即联系客服。", ch, when, d["old"], d["new"])
		}
		if en {
			return "Contact added", fmt.Sprintf("The %s %s was added to your account at %s. If this was not you, contact support now.",
				ch, d["new"], when)
		}
		return "已绑定新身份", fmt.Sprintf("您的账户已于 %s 绑定%s %s。如非本人操作，请立即联系客服。", when, ch, d["new"])
	case NoticePasswordChanged:
		if d["via_reset"] == "true" {
			if en {
				return "Password reset", fmt.Sprintf("Your password was reset at %s and every device was signed out. "+
					"Withdrawals need manual review for the next 24 hours. If this was not you, contact support now.", when)
			}
			return "密码已重置", fmt.Sprintf("您的密码已于 %s 重置，所有设备均已退出登录；此后 24 小时内提现需人工审核。如非本人操作，请立即联系客服。", when)
		}
		if en {
			return "Password changed", fmt.Sprintf("Your password was changed at %s and other devices were signed out. "+
				"If this was not you, reset your password now.", when)
		}
		return "密码已修改", fmt.Sprintf("您的密码已于 %s 修改，其他设备均已退出登录。如非本人操作，请立即重置密码。", when)
	case NoticeAccountLocked:
		if en {
			return "Sign-in locked", fmt.Sprintf("Too many wrong passwords (last from IP %s): password sign-in is locked for 15 minutes from %s. "+
				"If this was not you, consider changing your password.", d["ip"], when)
		}
		return "登录已临时锁定", fmt.Sprintf("密码错误次数过多（最近一次来自 IP %s），自 %s 起 15 分钟内无法用密码登录。如非本人操作，建议修改密码。",
			d["ip"], when)
	case NoticeTOTPChanged:
		if d["enabled"] == "true" {
			if en {
				return "Authenticator app bound", fmt.Sprintf("An authenticator app was bound to your account at %s; "+
					"security checks now use its codes. If this was not you, contact support now.", when)
			}
			return "已绑定身份验证器", fmt.Sprintf("您的账户已于 %s 绑定身份验证器，之后的安全验证将使用它生成的验证码。如非本人操作，请立即联系客服。", when)
		}
		if en {
			return "Authenticator app removed", fmt.Sprintf("The authenticator app was removed from your account at %s; "+
				"for 24 hours after it, withdrawals are reviewed by staff. If this was not you, contact support now.", when)
		}
		return "已解绑身份验证器", fmt.Sprintf("您的账户已于 %s 解绑身份验证器，此后 24 小时内提现需人工审核。如非本人操作，请立即联系客服。", when)
	case NoticeDepositCredited:
		if en {
			return "Deposit credited", fmt.Sprintf("%s %s from %s (transaction %s) was credited to your spot account at %s.",
				d["amount"], d["asset"], d["network"], d["tx"], when)
		}
		return "充值已到账", fmt.Sprintf("您的 %s %s 充值（%s，交易 %s）已于 %s 存入现货账户。", d["amount"], d["asset"], d["network"], d["tx"], when)
	case NoticeDepositUnclaimed:
		why := pick(depositReasons, d["reason"], en)
		amount := strings.TrimSpace(d["amount"] + " " + d["asset"])
		if en {
			return "Deposit not credited", fmt.Sprintf("A deposit of %s on %s (transaction %s) was not credited to your account because %s. "+
				"It is held for manual review; recovery is not guaranteed and may carry a fee. Contact support with the transaction hash.",
				amount, d["network"], d["tx"], why)
		}
		return "充值未入账", fmt.Sprintf("您在 %s 上的一笔充值 %s（交易 %s）未能存入账户，原因：%s。资金已转入待处理，需人工审核；平台不承诺找回，找回可能收取手续费，请凭交易哈希联系客服。",
			d["network"], amount, d["tx"], why)
	case NoticeWithdrawalRequested:
		if en {
			return "Withdrawal requested", fmt.Sprintf("A withdrawal of %s %s to %s (%s) was requested at %s; the amount and the fee %s are frozen. "+
				"If this was not you, cancel it on the withdrawal page and change your password now.", d["amount"], d["asset"], d["address"], d["network"], when, d["fee"])
		}
		return "提现申请已提交", fmt.Sprintf("您于 %s 申请提现 %s %s 到 %s（%s），金额与手续费 %s 已冻结。如非本人操作，请立即在提现页撤销并修改密码。",
			when, d["amount"], d["asset"], d["address"], d["network"], d["fee"])
	case NoticeWithdrawalCompleted:
		if en {
			if d["internal"] == "true" {
				return "Withdrawal completed", fmt.Sprintf("Your withdrawal of %s %s to %s was completed inside the platform at %s.", d["amount"], d["asset"], d["address"], when)
			}
			return "Withdrawal completed", fmt.Sprintf("Your withdrawal of %s %s to %s (%s) was confirmed on chain at %s (transaction %s).",
				d["amount"], d["asset"], d["address"], d["network"], when, d["tx"])
		}
		if d["internal"] == "true" {
			return "提现已完成", fmt.Sprintf("您的 %s %s 提现（到 %s）已于 %s 在平台内完成划转。", d["amount"], d["asset"], d["address"], when)
		}
		return "提现已完成", fmt.Sprintf("您的 %s %s 提现（到 %s，%s）已于 %s 在链上确认，交易 %s。", d["amount"], d["asset"], d["address"], d["network"], when, d["tx"])
	case NoticeWithdrawalRejected:
		if en {
			return "Withdrawal rejected", fmt.Sprintf("Your withdrawal of %s %s to %s was rejected at %s (%s); the frozen funds are released.",
				d["amount"], d["asset"], d["address"], when, d["reason"])
		}
		return "提现未通过", fmt.Sprintf("您的 %s %s 提现（到 %s）已于 %s 被拒绝（%s），冻结资金已退回。", d["amount"], d["asset"], d["address"], when, d["reason"])
	case NoticeWithdrawalCanceled:
		if en {
			return "Withdrawal canceled", fmt.Sprintf("Your withdrawal of %s %s was canceled at %s; the frozen funds are released.", d["amount"], d["asset"], when)
		}
		return "提现已撤销", fmt.Sprintf("您的 %s %s 提现已于 %s 撤销，冻结资金已退回。", d["amount"], d["asset"], when)
	case NoticeWithdrawalFailed:
		if en {
			return "Withdrawal failed", fmt.Sprintf("Your withdrawal of %s %s to %s failed at %s; support will handle it. Reference %s.",
				d["amount"], d["asset"], d["address"], when, d["id"])
		}
		return "提现失败", fmt.Sprintf("您的 %s %s 提现（到 %s）于 %s 失败，客服会跟进处理，编号 %s。", d["amount"], d["asset"], d["address"], when, d["id"])
	case NoticeMarginWarned:
		acct := marginAccount(d["account_type"], d["symbol"], en)
		if en {
			return "Margin level warning", fmt.Sprintf("The margin level of your %s fell to %s at %s (warning level %s, liquidation at %s). "+
				"Move assets in or repay to keep it from being liquidated.", acct, d["margin_level"], when, d["warn_level"], d["liquidation_level"])
		}
		return "杠杆账户风险率预警", fmt.Sprintf("您的%s风险率已于 %s 降至 %s（预警线 %s，强平线 %s）。请划入资产或还币，以免被强平。",
			acct, when, d["margin_level"], d["warn_level"], d["liquidation_level"])
	case NoticeMarginLiquidating:
		acct := marginAccount(d["account_type"], d["symbol"], en)
		if en {
			return "Margin account being liquidated", fmt.Sprintf("Your %s reached its liquidation level at %s (margin level %s): "+
				"its open orders are canceled, its assets sold at market and its debts repaid.", acct, when, d["margin_level"])
		}
		return "杠杆账户正在强平", fmt.Sprintf("您的%s风险率 %s 已于 %s 到达强平线：系统撤销其挂单，以市价卖出资产并归还负债。", acct, d["margin_level"], when)
	case NoticeMarginLiquidated:
		acct := marginAccount(d["account_type"], d["symbol"], en)
		if en {
			return "Margin liquidation completed", fmt.Sprintf("The liquidation of your %s completed at %s: repaid %s, liquidation fee %s USDT, "+
				"insurance fund %s USDT; left in the account: %s.", acct, when, orNone(d["repaid"], en), d["fee"], d["insurance_covered"], orNone(d["remaining"], en))
		}
		return "杠杆账户强平完成", fmt.Sprintf("您的%s已于 %s 完成强平：归还 %s，强平费 %s USDT，保险基金补足 %s USDT；账户剩余 %s。",
			acct, when, orNone(d["repaid"], en), d["fee"], d["insurance_covered"], orNone(d["remaining"], en))
	case NoticeStatusChanged:
		to := pick(statusNames, d["to"], en)
		if en {
			return "Account status changed", fmt.Sprintf("Your account status changed to %s at %s. Contact support if you have questions.", to, when)
		}
		return "账户状态变更", fmt.Sprintf("您的账户状态已于 %s 变更为：%s。如有疑问请联系客服。", when, to)
	}
	return in.Type, ""
}

// orNone is a list of amounts, or "none" for an empty one.
func orNone(s string, en bool) string {
	switch {
	case s != "":
		return s
	case en:
		return "none"
	default:
		return "无"
	}
}

// NoticeMail wraps a notice for mail or SMS, signed with brand
// (DefaultBrand when empty). Mails carry the user's anti-phishing code
// when set; SMS carry no links and only the title.
func NoticeMail(ch Channel, to, title, body, antiPhishing, lang, brand string) Message {
	brand = brandOr(brand)
	m := Message{Channel: ch, To: to}
	if ch == ChannelSMS {
		m.Text = fmt.Sprintf("【%s】%s", brand, title)
		if english(lang) {
			m.Text = fmt.Sprintf("[%s] %s", brand, title)
		}
		return m
	}
	var b strings.Builder
	if english(lang) {
		m.Subject = fmt.Sprintf("[%s] %s", brand, title)
		if antiPhishing != "" {
			fmt.Fprintf(&b, "Anti-phishing code: %s\n\n", antiPhishing)
		}
		b.WriteString(body)
		b.WriteString("\n\nThis is a security notice; do not reply.")
	} else {
		m.Subject = fmt.Sprintf("【%s】%s", brand, title)
		if antiPhishing != "" {
			fmt.Fprintf(&b, "防钓鱼码：%s\n\n", antiPhishing)
		}
		b.WriteString(body)
		b.WriteString("\n\n此为安全通知，请勿回复。")
	}
	m.Text = b.String()
	return m
}
