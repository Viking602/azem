package contextarchive

import (
	"bytes"
	"image"
	"image/png"
	"reflect"
	"strings"
	"testing"

	"github.com/Viking602/venat/message"
)

func TestBuildRendersDeterministicCJKArchiveAndRoundTripsSource(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleUser, "修复压缩失败：必须保留中文、命令、错误和工具证据。"),
		message.NewText(message.RoleAssistant, strings.Repeat("归档内容含中文与 ASCII path=/tmp/example。\n", 900)),
	}
	first, err := Build(history, Options{Visual: true, MaxFrames: 2, HeadRunes: 32, TailRunes: 32})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(history, Options{Visual: true, MaxFrames: 2, HeadRunes: 32, TailRunes: 32})
	if err != nil {
		t.Fatal(err)
	}
	if first.Manifest.Carrier != "bitmap" || first.Manifest.FrameCount == 0 || first.Manifest.SourceSHA256 == "" {
		t.Fatalf("manifest=%+v", first.Manifest)
	}
	if !reflect.DeepEqual(first.Manifest, second.Manifest) {
		t.Fatalf("manifest is not deterministic:\nfirst=%+v\nsecond=%+v", first.Manifest, second.Manifest)
	}
	for index := range first.Frames {
		if !bytes.Equal(first.Frames[index].Bytes, second.Frames[index].Bytes) {
			t.Fatalf("frame %d is not deterministic", index)
		}
		decoded, decodeErr := png.Decode(bytes.NewReader(first.Frames[index].Bytes))
		if decodeErr != nil {
			t.Fatalf("decode frame %d: %v", index, decodeErr)
		}
		if decoded.Bounds().Dx() != frameWidth || decoded.Bounds().Dy() != frameHeight {
			t.Fatalf("frame dimensions=%v", decoded.Bounds())
		}
		gray, ok := decoded.(*image.Gray)
		if !ok {
			t.Fatalf("frame %d decoded as %T, want grayscale archive", index, decoded)
		}
		darkPixels := 0
		for _, pixel := range gray.Pix {
			if pixel < 240 {
				darkPixels++
			}
		}
		if darkPixels < 100 {
			t.Fatalf("frame %d has only %d rendered text pixels", index, darkPixels)
		}
	}
	source, err := DecodeSource(first.Source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(source.Messages, history) {
		t.Fatalf("round trip changed history")
	}
}

func TestBuildCapsFramesButKeepsCompleteRecoverableSource(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleUser, "oldest-marker "+strings.Repeat("旧证据甲乙丙丁 ", 25_000)+" newest-marker"),
	}
	result, err := Build(history, Options{Visual: true, MaxFrames: 2, MaxPayloadBytes: 8 << 20, HeadRunes: 16, TailRunes: 16})
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.TotalPages <= result.Manifest.FrameCount || result.Manifest.FrameCount != 2 || result.Manifest.TruncatedChars <= 0 {
		t.Fatalf("manifest=%+v", result.Manifest)
	}
	if result.Manifest.FramePages[0] != 0 || result.Manifest.FramePages[1] != result.Manifest.TotalPages-1 {
		t.Fatalf("selected pages=%v total=%d", result.Manifest.FramePages, result.Manifest.TotalPages)
	}
	source, err := DecodeSource(result.Source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source.Messages[0].Text, "oldest-marker") || !strings.Contains(source.Messages[0].Text, "newest-marker") {
		t.Fatalf("recoverable source lost edge markers")
	}
}

func TestBuildEnforcesFixedFrameAndPayloadCaps(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleUser, strings.Repeat("中文 CJK command=/tmp/archive error=failed\n", 30_000)),
	}
	result, err := Build(history, Options{Visual: true, MaxFrames: 100, MaxPayloadBytes: 100 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.FrameCount > defaultMaxFrames {
		t.Fatalf("frame count=%d exceeds cap=%d", result.Manifest.FrameCount, defaultMaxFrames)
	}
	if result.Manifest.FrameBytes > defaultMaxPayloadBytes {
		t.Fatalf("frame bytes=%d exceeds cap=%d", result.Manifest.FrameBytes, defaultMaxPayloadBytes)
	}
}

func TestBuildRejectsOversizedSourceBeforeRendering(t *testing.T) {
	history := []message.Message{message.NewText(message.RoleUser, strings.Repeat("x", 1_025))}
	_, err := Build(history, Options{Visual: true, MaxSourceBytes: 1_024})
	if err == nil || !strings.Contains(err.Error(), "source fields exceed 1024-byte limit") {
		t.Fatalf("oversized source error = %v", err)
	}
}

func TestBuildFallsBackToArtifactWhenOneFrameExceedsPayloadBudget(t *testing.T) {
	history := []message.Message{
		message.NewText(message.RoleUser, strings.Repeat("payload ", 500)),
	}
	result, err := Build(history, Options{Visual: true, MaxPayloadBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.Carrier != "artifact" || result.Manifest.FrameCount != 0 || len(result.Frames) != 0 {
		t.Fatalf("result=%+v manifest=%+v", result, result.Manifest)
	}
}

func TestBuildTextOnlyCarrierKeepsBoundedEdges(t *testing.T) {
	history := []message.Message{message.NewText(message.RoleUser, "BEGIN-"+strings.Repeat("内容", 200)+"-END")}
	result, err := Build(history, Options{Visual: false, HeadRunes: 12, TailRunes: 12})
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.Carrier != "artifact" || len(result.Frames) != 0 || len([]rune(result.Head)) != 12 || len([]rune(result.Tail)) != 12 {
		t.Fatalf("result=%+v manifest=%+v", result, result.Manifest)
	}
	if result.Manifest.TruncatedChars <= 0 {
		t.Fatalf("expected bounded text carrier, manifest=%+v", result.Manifest)
	}
}

func BenchmarkBuild(b *testing.B) {
	history := make([]message.Message, 0, 120)
	for range 60 {
		history = append(
			history,
			message.NewText(message.RoleUser, "修复归档边界、保留工具证据并核对 source hash。"),
			message.NewText(message.RoleAssistant, strings.Repeat("中文 CJK path=/tmp/archive error=failed evidence=durable\n", 40)),
		)
	}
	for _, benchmark := range []struct {
		name    string
		options Options
	}{
		{name: "ArtifactCarrier", options: Options{Visual: false}},
		{name: "BitmapCarrier", options: Options{Visual: true}},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			var result Result
			var err error
			b.ResetTimer()
			for range b.N {
				result, err = Build(history, benchmark.options)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(result.Source)), "source_B/op")
			b.ReportMetric(float64(result.Manifest.FrameBytes), "frame_B/op")
		})
	}
}
