package domain

import (
	"strings"
	"testing"
)

func TestOTPMessage(t *testing.T) {
	zh := OTPMessage(ChannelEmail, "a@mail.test", "123456", "REGISTER", "zh-CN", 5)
	if !strings.Contains(zh.Subject, "123456") || !strings.Contains(zh.Text, "注册") || !strings.Contains(zh.Text, "5 分钟") {
		t.Fatalf("zh mail: %+v", zh)
	}
	en := OTPMessage(ChannelEmail, "a@mail.test", "123456", "LOGIN", "en-US", 5)
	if !strings.Contains(en.Text, "sign-in") || !strings.Contains(en.Subject, "123456") {
		t.Fatalf("en mail: %+v", en)
	}
	sms := OTPMessage(ChannelSMS, "+8613812341234", "654321", "PASSWORD_RESET", "zh-CN", 5)
	if !strings.Contains(sms.Text, "654321") || strings.Contains(sms.Text, "http") || len([]rune(sms.Text)) > 70 {
		t.Fatalf("SMS must be short, linkless and carry the code: %q", sms.Text)
	}
	unknown := OTPMessage(ChannelEmail, "a@mail.test", "123456", "SOMETHING", "zh-CN", 5)
	if !strings.Contains(unknown.Text, "验证") {
		t.Fatalf("unknown scenes fall back to a generic purpose: %q", unknown.Text)
	}
}

func TestParseChannelAndClass(t *testing.T) {
	if c, err := ParseChannel("SMS"); err != nil || c != ChannelSMS {
		t.Fatal(c, err)
	}
	if _, err := ParseChannel("sms"); err == nil {
		t.Fatal("channels are upper case on the wire")
	}
	if ClassOf(&SendError{Class: FailureTimeout}) != FailureTimeout || ClassOf(nil) != FailureUnknown {
		t.Fatal("ClassOf")
	}
}
