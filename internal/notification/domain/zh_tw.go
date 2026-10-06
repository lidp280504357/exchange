package domain

import "strings"

// Traditional Chinese (design 2026-10-06 繁体中文 §2.2): the messages are
// written in Simplified Chinese and English; a Traditional reader gets each
// Simplified text in the sites' Traditional wording, converted by the web's
// generator (OpenCC s2twp and the term table) into zhTWPairs (zh_tw_gen.go,
// pnpm i18n in web, checked by task web:check).

// traditional reports a reader of Traditional Chinese: zh-Hant, or Taiwan,
// Hong Kong or Macao, as the sites decide (core localeOf).
func traditional(lang string) bool {
	l := strings.ToLower(lang)
	switch {
	case !strings.HasPrefix(l, "zh"):
		return false
	case strings.Contains(l, "-hant"):
		return true
	case strings.Contains(l, "-hans"):
		return false
	}
	for _, region := range []string{"zh-tw", "zh-hk", "zh-mo"} {
		if l == region || strings.HasPrefix(l, region+"-") {
			return true
		}
	}
	return false
}

// zhTW looks the Traditional wording up by the Simplified text.
var zhTW = func() map[string]string {
	m := make(map[string]string, len(zhTWPairs))
	for _, p := range zhTWPairs {
		m[p[0]] = p[1]
	}
	return m
}()

// zh returns s, a Simplified Chinese text of the messages, in the reader's
// Chinese: its Traditional wording for a Traditional reader, as written
// otherwise (and for a text the generator has not seen).
func zh(lang, s string) string {
	if traditional(lang) {
		if tw, ok := zhTW[s]; ok {
			return tw
		}
	}
	return s
}
