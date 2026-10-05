package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExcerptIsTheFirstParagraphAsText(t *testing.T) {
	for body, want := range map[string]string{
		"## 维护时间\n\n10 月 3 日 02:00 起**暂停充值**，详见[充值说明](/help/deposit)。\n\n第二段":                                 "10 月 3 日 02:00 起暂停充值，详见充值说明。",
		"- a list\n- of items\n\n> a quote\n\n| a | table |\n\n---\n\nThe `first`\nparagraph ![logo](/x.png)": "The first paragraph logo",
		"1. step one\n\n```\ncode\n```\n\nAfter the code.":                                                    "After the code.",
		"# Only a heading": "",
		"":                 "",
	} {
		if got := Excerpt(body, false); got != want {
			t.Errorf("Excerpt(%q) = %q, want %q", body, got, want)
		}
	}
	long := Excerpt(strings.Repeat("长", 300), true)
	if utf8.RuneCountInString(long) != excerptMax || !strings.HasSuffix(long, "…") {
		t.Fatalf("a long paragraph: %d runes %q", utf8.RuneCountInString(long), long[len(long)-6:])
	}
}

// A summary taken from the body is the first paragraph the mode shows, not
// a mode block's marker or the other mode's text (review BK).
func TestExcerptByMode(t *testing.T) {
	body := ":::test\n本站处于**测试模式**，资金均为模拟。\n:::\n\n:::formal\n充值前请核对网络。\n:::\n\n第二段"
	if got := Excerpt(body, true); got != "本站处于测试模式，资金均为模拟。" {
		t.Errorf("test mode: %q", got)
	}
	if got := Excerpt(body, false); got != "充值前请核对网络。" {
		t.Errorf("live: %q", got)
	}
	if got := Excerpt(":::test\n测试段落\n:::\n通用段落", false); got != "通用段落" {
		t.Errorf("after the other mode's block: %q", got)
	}
}

// InMode keeps what core's renderByMode keeps (design 2026-10-04 §4.4).
func TestInMode(t *testing.T) {
	for _, c := range []struct {
		body string
		test bool
		want string
	}{
		{"a\n:::test\nb\n:::\nc", true, "a\nb\nc"},
		{"a\n:::test\nb\n:::\nc", false, "a\nc"},
		{"a\r\n  :::formal  \r\nb\r\n:::\r\nc", false, "a\nb\nc"},
		{":::formal\nb", false, "b"}, // an unclosed block runs to the end
		{":::formal\nb", true, ""},
		{":::test\n:::formal\nx\n:::\ny", true, ":::formal\nx\ny"}, // no nesting: a marker in a block is text
		{":::test\n:::formal\nx\n:::\ny", false, "y"},
		{":::note\nx\n:::", false, ":::note\nx\n:::"},                          // other containers stay
		{"```md\n:::test\n```\n:::test\nz\n:::", false, "```md\n:::test\n```"}, // fenced code stays
		{"~~~~\n~~~\n:::test\n~~~~\nw", false, "~~~~\n~~~\n:::test\n~~~~\nw"},  // closed by as many
		{"    :::test\nv", false, "    :::test\nv"},                            // indented code
	} {
		if got := InMode(c.body, c.test); got != c.want {
			t.Errorf("InMode(%q, %v) = %q, want %q", c.body, c.test, got, c.want)
		}
	}
}

// The fixed sections (design 2026-10-04 §4.4) take only their own slugs.
func TestFixedSectionSlugs(t *testing.T) {
	text := []ArticleText{{Locale: LocaleZH, Title: "条款", Body: "正文"}}
	for _, ok := range []Article{
		{Section: SectionLegal, Slug: "terms", Modes: ModeBoth, Texts: text},
		{Section: SectionLegal, Slug: "contact", Modes: ModeTest, Texts: text},
		{Section: SectionHome, Slug: "home-hero", Modes: ModeFormal, Texts: text},
		{Section: SectionHelp, Slug: "anything-goes", Modes: ModeBoth, Texts: text},
	} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%s/%s: %v", ok.Section, ok.Slug, err)
		}
	}
	for _, bad := range []Article{
		{Section: SectionLegal, Slug: "cookies", Modes: ModeBoth, Texts: text},
		{Section: SectionHome, Slug: "banner", Modes: ModeBoth, Texts: text},
		{Section: "BLOG", Slug: "terms", Modes: ModeBoth, Texts: text},
		{Section: SectionLegal, Slug: "terms", Texts: text},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s/%s accepted", bad.Section, bad.Slug)
		}
	}
	if len(FixedSlugs(SectionLegal)) != 6 || FixedSlugs(SectionHelp) != nil {
		t.Fatalf("fixed slugs %v %v", FixedSlugs(SectionLegal), FixedSlugs(SectionHelp))
	}
}
