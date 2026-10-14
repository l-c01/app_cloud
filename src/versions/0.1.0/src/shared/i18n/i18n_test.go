package i18n

import (
	"strings"
	"testing"
)

func catalog(lang string, messages map[string]string) *Catalog {
	return &Catalog{SchemaVersion: SchemaVersion, Language: lang, DisplayName: lang, Messages: messages}
}

func testBundle(t *testing.T) *Bundle {
	t.Helper()
	b := NewBundle("en")
	if err := b.Add(catalog("zh-CN", map[string]string{
		"greeting":        "你好，{0}",
		"count.one":       "有 {0} 项",
		"count.other":     "有 {0} 项",
		"only.zh":         "仅中文",
		"two.args":        "{0} 与 {1}",
		"missing.arg":     "{0} 和 {1}",
		"bad.placeholder": "{oops}",
	})); err != nil {
		t.Fatalf("Add zh-CN: %v", err)
	}
	if err := b.Add(catalog("en", map[string]string{
		"greeting":        "Hello, {0}",
		"count.one":       "{0} item",
		"count.other":     "{0} items",
		"two.args":        "{0} and {1}",
		"missing.arg":     "{0}",
		"bad.placeholder": "{oops}",
	})); err != nil {
		t.Fatalf("Add en: %v", err)
	}
	return b
}

func TestFormatReplacesPositionalPlaceholders(t *testing.T) {
	if got := Format("Hello, {0}", "world"); got != "Hello, world" {
		t.Errorf("got %q", got)
	}
	if got := Format("{0} and {1}", "a", "b"); got != "a and b" {
		t.Errorf("got %q", got)
	}
	// 同一个占位符可以重复出现。
	if got := Format("{0}-{0}", "x"); got != "x-x" {
		t.Errorf("got %q", got)
	}
}

func TestFormatLeavesOutOfRangePlaceholdersVisible(t *testing.T) {
	// 越界占位符必须原样保留：界面会显示 {2}，让翻译缺陷立刻可见，
	// 而不是静默变成空串。
	got := Format("a {0} b {2}", "x")
	if !strings.Contains(got, "{2}") {
		t.Errorf("an out-of-range placeholder should stay visible, got %q", got)
	}
	if !strings.Contains(got, "a x b") {
		t.Errorf("the in-range placeholder should still be replaced, got %q", got)
	}
}

func TestFormatIgnoresNonNumericPlaceholders(t *testing.T) {
	// 命名占位符不是本包的语法，应原样保留而不是猜。
	got := Format("value {name}", "x")
	if got != "value {name}" {
		t.Errorf("got %q", got)
	}
}

func TestFormatHandlesUnterminatedBrace(t *testing.T) {
	if got := Format("open {0", "x"); got != "open {0" {
		t.Errorf("got %q", got)
	}
}

func TestFormatWithoutArgsIsPassthrough(t *testing.T) {
	if got := Format("no placeholders"); got != "no placeholders" {
		t.Errorf("got %q", got)
	}
}

func TestCountPlaceholders(t *testing.T) {
	cases := map[string]int{
		"":            0,
		"plain":       0,
		"{0}":         1,
		"{0} {1}":     2,
		"{0} {0}":     2,
		"{name}":      0,
		"{0":          0,
		"{0} and {x}": 1,
	}
	for input, want := range cases {
		if got := CountPlaceholders(input); got != want {
			t.Errorf("CountPlaceholders(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestNegotiateExactMatch(t *testing.T) {
	b := testBundle(t)
	if got := b.Negotiate("zh-CN"); got != "zh-cn" {
		t.Errorf("got %q", got)
	}
	if got := b.Negotiate("en"); got != "en" {
		t.Errorf("got %q", got)
	}
}

func TestNegotiateByPrimarySubtag(t *testing.T) {
	b := testBundle(t)
	// zh-Hans / zh-TW 都应落到已加载的 zh-cn。
	for _, want := range []string{"zh-Hans", "zh-TW", "zh"} {
		if got := b.Negotiate(want); got != "zh-cn" {
			t.Errorf("Negotiate(%q) = %q, want zh-cn", want, got)
		}
	}
	// en-GB 落到 en。
	if got := b.Negotiate("en-GB"); got != "en" {
		t.Errorf("got %q", got)
	}
}

func TestNegotiateFallsBackForUnknownLanguage(t *testing.T) {
	b := testBundle(t)
	if got := b.Negotiate("fr"); got != "en" {
		t.Errorf("got %q, want the fallback en", got)
	}
}

func TestNegotiateReturnsSomethingWhenOnlyOneLanguageExists(t *testing.T) {
	b := NewBundle("en")
	if err := b.Add(catalog("de", map[string]string{"x": "y"})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 兜底语言不存在时也应返回一个已加载的语言，而不是空串。
	if got := b.Negotiate("fr"); got != "de" {
		t.Errorf("got %q, want de", got)
	}
}

func TestNegotiateEmptyBundle(t *testing.T) {
	b := NewBundle("en")
	if got := b.Negotiate("zh-CN"); got != "" {
		t.Errorf("got %q, want empty for an empty bundle", got)
	}
}

func TestTranslateUsesRequestedLanguage(t *testing.T) {
	b := testBundle(t)
	if got := b.T("zh-CN", "greeting", "世界"); got != "你好，世界" {
		t.Errorf("got %q", got)
	}
	if got := b.T("en", "greeting", "world"); got != "Hello, world" {
		t.Errorf("got %q", got)
	}
}

func TestTranslateFallsBackToFallbackLanguage(t *testing.T) {
	b := testBundle(t)

	// 中文里有的键走中文。
	if got := b.T("zh-CN", "count.other", 3); got != "有 3 项" {
		t.Errorf("got %q", got)
	}

	// 重新加载只含部分键的英文表，模拟「中文缺该键」的情况。
	if err := b.Add(catalog("en", map[string]string{
		"greeting": "Hello, {0}",
		"extra":    "English only",
	})); err != nil {
		t.Fatalf("Add en: %v", err)
	}
	// 中文里没有、英文里有的键应回落到英文。
	if got := b.T("zh-CN", "extra"); got != "English only" {
		t.Errorf("got %q, want the English fallback", got)
	}
}

func TestTranslateReturnsKeyWhenMissingEverywhere(t *testing.T) {
	// 缺键必须可见：返回键名而不是空串，否则界面会出现难以察觉的空白。
	b := testBundle(t)
	key := "this.key.does.not.exist"
	if got := b.T("zh-CN", key); got != key {
		t.Errorf("got %q, want the key itself", got)
	}
}

func TestTranslatePluralForms(t *testing.T) {
	b := testBundle(t)
	if got := b.Tn("en", "count", 1, 1); got != "1 item" {
		t.Errorf("singular: got %q", got)
	}
	if got := b.Tn("en", "count", 5, 5); got != "5 items" {
		t.Errorf("plural: got %q", got)
	}
	if got := b.Tn("zh-CN", "count", 5, 5); got != "有 5 项" {
		t.Errorf("zh plural: got %q", got)
	}
}

func TestTranslatePluralFallsBackToBaseKey(t *testing.T) {
	b := NewBundle("en")
	if err := b.Add(catalog("en", map[string]string{"simple": "value {0}"})); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// 没有 .one/.other 时回退到基础键。
	if got := b.Tn("en", "simple", 2, 7); got != "value 7" {
		t.Errorf("got %q", got)
	}
}

func TestMissingKeys(t *testing.T) {
	b := testBundle(t)
	missing := b.MissingKeys([]string{"greeting", "nope", "also.nope"})
	if len(missing) != 2 {
		t.Fatalf("missing = %v", missing)
	}
	if missing[0] != "also.nope" || missing[1] != "nope" {
		t.Errorf("missing should be sorted: %v", missing)
	}
}

func TestInconsistentKeysDetectsPlaceholderMismatch(t *testing.T) {
	b := testBundle(t)
	inconsistent := b.InconsistentKeys([]string{"greeting", "missing.arg", "two.args"})
	// missing.arg 在中文有两个占位符、英文只有一个 → 必须被检出。
	if _, found := inconsistent["missing.arg"]; !found {
		t.Errorf("missing.arg should be reported as inconsistent: %v", inconsistent)
	}
	if _, found := inconsistent["greeting"]; found {
		t.Errorf("greeting is consistent and should not be reported: %v", inconsistent)
	}
}

func TestCatalogValidation(t *testing.T) {
	cases := []struct {
		name string
		c    *Catalog
		ok   bool
	}{
		{"valid", catalog("en", map[string]string{"a": "b"}), true},
		{"no language", catalog("", map[string]string{"a": "b"}), false},
		{"no messages", catalog("en", nil), false},
		{"wrong schema", &Catalog{Language: "en", SchemaVersion: "9.9.9", Messages: map[string]string{"a": "b"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.c.Validate()
			if tc.ok && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected a validation error")
			}
		})
	}
	if err := (*Catalog)(nil).Validate(); err == nil {
		t.Error("a nil catalog must be rejected")
	}
}

func TestLoadJSONRejectsMalformedCatalog(t *testing.T) {
	b := NewBundle("en")
	if err := b.LoadJSON([]byte("{not json")); err == nil {
		t.Error("malformed JSON must be rejected")
	}
	if err := b.LoadJSON([]byte(`{"language":"en","messages":{}}`)); err == nil {
		t.Error("a catalog without messages must be rejected")
	}
	if err := b.LoadJSON([]byte(`{"language":"en","messages":{"a":"b"}}`)); err != nil {
		t.Errorf("a valid catalog should load: %v", err)
	}
	if !b.Has("en") {
		t.Error("en should be loaded")
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"zh_CN":   "zh-cn",
		" ZH-CN ": "zh-cn",
		"en":      "en",
		"EN_us":   "en-us",
	}
	for input, want := range cases {
		if got := Normalize(input); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLanguagesAreSorted(t *testing.T) {
	b := testBundle(t)
	langs := b.Languages()
	if len(langs) != 2 || langs[0] != "en" || langs[1] != "zh-cn" {
		t.Errorf("languages = %v", langs)
	}
}

func TestResolveLoadsAndNegotiates(t *testing.T) {
	b, lang, err := Resolve("zh-Hans", "en", map[string][]byte{
		"en":    []byte(`{"language":"en","messages":{"a":"b"}}`),
		"zh-CN": []byte(`{"language":"zh-CN","messages":{"a":"c"}}`),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if lang != "zh-cn" {
		t.Errorf("lang = %q", lang)
	}
	if got := b.T(lang, "a"); got != "c" {
		t.Errorf("got %q", got)
	}
}

func TestResolveReportsWhichCatalogFailed(t *testing.T) {
	_, _, err := Resolve("en", "en", map[string][]byte{
		"en": []byte("broken"),
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "en") {
		t.Errorf("error should name the failing catalog: %v", err)
	}
}
