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
		if got := Excerpt(body); got != want {
			t.Errorf("Excerpt(%q) = %q, want %q", body, got, want)
		}
	}
	long := Excerpt(strings.Repeat("长", 300))
	if utf8.RuneCountInString(long) != excerptMax || !strings.HasSuffix(long, "…") {
		t.Fatalf("a long paragraph: %d runes %q", utf8.RuneCountInString(long), long[len(long)-6:])
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
