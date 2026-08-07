package desktop

import (
	"reflect"
	"runtime"
	"testing"
)

func TestSystemFontFamiliesAvailableWithoutCGO(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("AppKit is only available on macOS")
	}
	fonts := systemFontFamilies("zh-CN")
	if len(fonts) == 0 {
		t.Fatal("expected AppKit system fonts")
	}
	found := false
	for _, font := range fonts {
		if font.Family == "PingFang SC" {
			found = true
			if font.Label != "苹方-简" {
				t.Fatalf("PingFang SC label = %q, want %q", font.Label, "苹方-简")
			}
		}
	}
	if !found {
		t.Fatal("PingFang SC not found")
	}
}

func TestNormalizeSystemFontFamilies(t *testing.T) {
	raw := " Songti SC\x1f宋体-简\x1fzh-Hans\x1e.Arabic UI\x1f阿拉伯界面\x1fzh\x1eArial\x1fArial\x1fen\x1eSONGTI SC\x1fSongti SC\x1fen\x1e"
	got := normalizeSystemFontFamilies(raw, "zh-CN")
	want := []SystemFont{{Family: "Arial", Label: "Arial"}, {Family: "Songti SC", Label: "宋体-简"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fonts = %#v, want %#v", got, want)
	}
}

func TestNormalizeSystemFontFamiliesUsesRequestedLanguage(t *testing.T) {
	got := normalizeSystemFontFamilies("Songti SC\x1f宋体-简\x1fzh-Hans\x1e", "en")
	want := []SystemFont{{Family: "Songti SC", Label: "Songti SC"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fonts = %#v, want %#v", got, want)
	}
}

func TestNormalizeFontconfigFamilies(t *testing.T) {
	raw := "宋体-简\x1fzh-cn\x1dSongti SC\x1fen\x1d\x1eArial\x1fen\x1d\x1e"
	got := normalizeFontconfigFamilies(raw, "zh-CN")
	want := []SystemFont{{Family: "Arial", Label: "Arial"}, {Family: "Songti SC", Label: "宋体-简"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fonts = %#v, want %#v", got, want)
	}
}
