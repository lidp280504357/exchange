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
