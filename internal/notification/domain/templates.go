package domain

import (
	"fmt"
	"strings"
)

// DefaultBrand names the exchange in a message while the platform
// profile's name cannot be read (design 2026-10-04 §4.5); every message
// carries the name, and SMS no links (§6.3).
const DefaultBrand = "Astras"

// brandOr is brand, or DefaultBrand when empty.
func brandOr(brand string) string {
	if brand == "" {
		return DefaultBrand
	}
	return brand
}

var sceneNames = map[string][2]string{ // zh-CN, en
	"REGISTER":         {"注册", "sign-up"},
	"LOGIN":            {"登录", "sign-in"},
	"LOGIN_CHALLENGE":  {"登录验证", "sign-in check"},
	"PASSWORD_RESET":   {"重置密码", "password reset"},
	"BIND_IDENTITY":    {"绑定新身份", "adding a contact"},
	"REBIND_IDENTITY":  {"更换身份", "changing a contact"},
	"STEP_UP":          {"安全验证", "security check"},
	"WITHDRAW_CONFIRM": {"确认提现", "withdrawal confirmation"},
}

func english(lang string) bool { return strings.HasPrefix(strings.ToLower(lang), "en") }

// OTPMessage renders the one-time code for scene in the user's language
// (zh-CN unless it starts with "en"), signed with brand (DefaultBrand
// when empty).
func OTPMessage(ch Channel, to, code, scene, lang string, ttlMinutes int, brand string) Message {
	brand = brandOr(brand)
	names, ok := sceneNames[scene]
	if !ok {
		names = [2]string{"验证", "verification"}
	}
	m := Message{Channel: ch, To: to}
	if english(lang) {
		m.Subject = fmt.Sprintf("[%s] Your verification code %s", brand, code)
		m.Text = fmt.Sprintf("Your %s code for %s is %s. It expires in %d minutes. "+
			"If you did not request it, ignore this message and never share the code.", brand, names[1], code, ttlMinutes)
		if ch == ChannelSMS {
			m.Text = fmt.Sprintf("[%s] Code %s for %s, valid %d min. Never share it.", brand, code, names[1], ttlMinutes)
		}
		return m
	}
	m.Subject = fmt.Sprintf("【%s】验证码 %s", brand, code)
	m.Text = fmt.Sprintf("您的%s验证码是 %s，用于%s，%d 分钟内有效。如非本人操作请忽略，切勿向任何人透露验证码。",
		brand, code, names[0], ttlMinutes)
	if ch == ChannelSMS {
		m.Text = fmt.Sprintf("【%s】验证码 %s，用于%s，%d 分钟内有效，请勿泄露。", brand, code, names[0], ttlMinutes)
	}
	return m
}
