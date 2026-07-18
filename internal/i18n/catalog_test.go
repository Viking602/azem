package i18n

import (
	"testing"
	"testing/fstest"
)

func TestEmbeddedCatalogsAndFormatting(t *testing.T) {
	c, err := New("zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.T("skill.name", map[string]string{"name": "verify"}); got != "技能：verify" {
		t.Fatalf("got %q", got)
	}
	if got := c.T("missing.key"); got != "[missing: missing.key]" {
		t.Fatalf("got %q", got)
	}
}

func TestStrictCatalogValidation(t *testing.T) {
	cases := []string{
		`{"a":"x","a":"y"}`,
		`{"":"x"}`,
		`{"a":""}`,
		`{"a":1}`,
	}
	for _, input := range cases {
		if _, err := decodeStrict([]byte(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	fsys := fstest.MapFS{
		"locales/en.json":    {Data: []byte(`{"a":"Hello {name}"}`)},
		"locales/zh-CN.json": {Data: []byte(`{"a":"你好"}`)},
	}
	if _, err := load(fsys); err == nil {
		t.Fatal("accepted mismatched placeholders")
	}
}
