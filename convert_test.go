package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadFixture(t *testing.T, path string) WhisperOutput {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var output WhisperOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return output
}

func TestLegacyOutputUnchangedWithoutSpeakers(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,960"},
			Text:       "看着天书的解读",
			Tokens: []Token{
				{Text: "[_BEG_]", Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:00,000"}},
				{Text: "看", Timestamps: Timestamp{From: "00:00:00,220", To: "00:00:00,280"}},
				{Text: "着", Timestamps: Timestamp{From: "00:00:00,280", To: "00:00:01,900"}},
			},
		},
		{
			Timestamps: Timestamp{From: "00:00:02,000", To: "00:00:02,500"},
			Text:       "第二句",
		},
	}}

	// Non-default options must be ignored for unlabelled input.
	options := DefaultOptions()
	options.SpeakerNames = map[string]string{"SPEAKER_00": "Alice"}
	options.PauseGap = time.Millisecond
	options.MaxChars = 1
	options.MaxDuration = time.Millisecond

	got, warnings, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	want := "1\n00:00:00,220 --> 00:00:01,900\n看着天书的解读\n\n" +
		"2\n00:00:02,000 --> 00:00:02,500\n第二句\n\n"
	if got != want {
		t.Fatalf("legacy output changed\nwant %q\ngot  %q", want, got)
	}
}

func TestSpeakerCuesFromTwoSpeakerFixture(t *testing.T) {
	output := loadFixture(t, "testdata/overlap_two_speakers.json")

	options := DefaultOptions()
	options.SpeakerNames = map[string]string{"SPEAKER_00": "Alice", "SPEAKER_01": "Bob"}

	got, warnings, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}

	// Segment 1: one Alice cue.
	// Segment 2: a 900ms pause splits Bob between "说说" and "重叠的".
	// Segment 3: per-token speakers split into Alice/Alice, Bob, Alice, and the
	// overlapping starts must sort stably by time with original order preserved
	// for equal starts.
	// Segment 4: a 1.6s pause splits Bob after "那".
	want := "1\n" +
		"00:00:00,200 --> 00:00:03,400\n" +
		"Alice: 大家好，今天聊一下字幕。\n\n" +
		"2\n" +
		"00:00:03,500 --> 00:00:05,300\n" +
		"Bob: 对，我想先说说\n\n" +
		"3\n" +
		"00:00:05,000 --> 00:00:05,500\n" +
		"Alice: 嗯，我\n\n" +
		"4\n" +
		"00:00:05,500 --> 00:00:06,100\n" +
		"Bob: 补充\n\n" +
		"5\n" +
		"00:00:06,100 --> 00:00:06,600\n" +
		"Alice: 一句。\n\n" +
		"6\n" +
		"00:00:06,200 --> 00:00:07,200\n" +
		"Bob: 重叠的部分。\n\n" +
		"7\n" +
		"00:00:09,000 --> 00:00:09,200\n" +
		"Bob: 那\n\n" +
		"8\n" +
		"00:00:10,800 --> 00:00:12,000\n" +
		"Bob: 我们继续。\n\n"

	if got != want {
		t.Fatalf("speaker SRT mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestOverlapKeepsTimecodesAndStableOrder(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:04,000"},
			Speaker:    "A",
			Tokens: []Token{
				{Text: "甲", Speaker: "A", Timestamps: Timestamp{From: "00:00:01,000", To: "00:00:03,000"}},
				{Text: "乙", Speaker: "B", Timestamps: Timestamp{From: "00:00:01,000", To: "00:00:02,000"}},
			},
		},
	}}

	options := DefaultOptions()
	options.SpeakerNames = map[string]string{"A": "Alice", "B": "Bob"}
	options.PauseGap = 0

	got, _, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	// Same start: original segment order is preserved (A before B), and both
	// cues keep their own overlapping end time.
	want := "1\n00:00:01,000 --> 00:00:03,000\nAlice: 甲\n\n" +
		"2\n00:00:01,000 --> 00:00:02,000\nBob: 乙\n\n"
	if got != want {
		t.Fatalf("overlap mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestCharAndDurationLimits(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:10,000"},
			Speaker:    "A",
			Tokens: []Token{
				{Text: "一二三", Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,000"}},
				{Text: "四五六", Timestamps: Timestamp{From: "00:00:01,000", To: "00:00:02,000"}},
				{Text: "七八九", Timestamps: Timestamp{From: "00:00:05,000", To: "00:00:09,000"}},
			},
		},
	}}

	options := DefaultOptions()
	options.PauseGap = 0
	options.MaxChars = 4
	options.MaxDuration = 3 * time.Second
	options.SpeakerNames = map[string]string{"A": "A"}

	got, _, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	// "一二三" (3 chars) + "四五六" would exceed MaxChars, so split.
	// "七八九" lasts 4s, exceeding MaxDuration, so it gets its own cue.
	want := "1\n00:00:00,000 --> 00:00:01,000\nA: 一二三\n\n" +
		"2\n00:00:01,000 --> 00:00:02,000\nA: 四五六\n\n" +
		"3\n00:00:05,000 --> 00:00:09,000\nA: 七八九\n\n"
	if got != want {
		t.Fatalf("limit mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestEmptyAndNoiseWordsAreDropped(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,000"},
			Speaker:    "A",
			Tokens: []Token{
				{Text: "", Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:00,100"}},
				{Text: "   ", Timestamps: Timestamp{From: "00:00:00,100", To: "00:00:00,200"}},
				{Text: "[_TT_1]", Timestamps: Timestamp{From: "00:00:00,200", To: "00:00:00,300"}},
				{Text: "好", Timestamps: Timestamp{From: "00:00:00,500", To: "00:00:00,800"}},
			},
		},
	}}

	options := DefaultOptions()
	options.SpeakerNames = map[string]string{"A": "A"}
	got, warnings, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	want := "1\n00:00:00,500 --> 00:00:00,800\nA: 好\n\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestMissingWordTimesAreInferred(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:03,000"},
			Speaker:    "A",
			Tokens: []Token{
				// Missing end: borrows the next word's start.
				{Text: "无", Timestamps: Timestamp{From: "00:00:00,400"}},
				// Missing both: spans previous end to next start.
				{Text: "时码", Timestamps: Timestamp{}},
				// Missing start: borrows the previous word's end.
				{Text: "词", Timestamps: Timestamp{To: "00:00:02,000"}},
				{Text: "尾", Timestamps: Timestamp{From: "00:00:02,000", To: "00:00:02,500"}},
			},
		},
	}}

	options := DefaultOptions()
	options.PauseGap = 0
	options.MaxChars = 3
	options.SpeakerNames = map[string]string{"A": "A"}
	got, warnings, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	// Missing boundaries are inferred forward: 无 borrows the next known start
	// (2.000), 时码 spans the gap, and 词 borrows the previous end (2.000).
	// MaxChars splits after three characters.
	want := "1\n00:00:00,400 --> 00:00:02,000\nA: 无时码\n\n" +
		"2\n00:00:02,000 --> 00:00:02,500\nA: 词尾\n\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSegmentFallbackWhenAllWordTimesMissing(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{From: "00:00:01,000", To: "00:00:02,000"},
			Speaker:    "A",
			Text:       "整段文本",
			Tokens: []Token{
				{Text: "无", Timestamps: Timestamp{}},
				{Text: "时码", Timestamps: Timestamp{}},
			},
		},
	}}

	got, _, err := Convert(output, DefaultOptions())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	// No usable word timestamps: both words inherit the segment range.
	if !strings.Contains(got, "00:00:01,000 --> 00:00:02,000") {
		t.Fatalf("expected segment time range, got:\n%s", got)
	}
}

func TestOffsetFallbackForMissingTimecodes(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{},
			Offsets:    &Offset{From: 1000, To: 2000},
			Speaker:    "A",
			Tokens: []Token{
				{Text: "偏移", Offsets: &Offset{From: 1200, To: 1800}},
			},
		},
	}}

	options := DefaultOptions()
	options.SpeakerNames = map[string]string{"A": "A"}
	got, warnings, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("expected no warnings, got %v", warnings)
	}
	want := "1\n00:00:01,200 --> 00:00:01,800\nA: 偏移\n\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUnknownSpeakerUsesRawIDWithWarning(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,000"},
			Tokens: []Token{
				{Text: "嗨", Speaker: "SPEAKER_09", Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,000"}},
			},
		},
	}}

	options := DefaultOptions()
	options.SpeakerNames = map[string]string{"SPEAKER_00": "Alice"}
	got, warnings, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got != "1\n00:00:00,000 --> 00:00:01,000\nSPEAKER_09: 嗨\n\n" {
		t.Fatalf("got %q", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `unknown speaker "SPEAKER_09"`) {
		t.Fatalf("expected one unknown-speaker warning, got %v", warnings)
	}
}

func TestUnlabelledWordsInLabelledFileGetDefaultSpeaker(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:02,000"},
			Tokens: []Token{
				{Text: "甲", Speaker: "A", Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,000"}},
				{Text: "乙", Timestamps: Timestamp{From: "00:00:01,000", To: "00:00:02,000"}},
			},
		},
	}}

	options := DefaultOptions()
	options.DefaultSpeaker = "访客"
	got, _, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	want := "1\n00:00:00,000 --> 00:00:01,000\nA: 甲\n\n" +
		"2\n00:00:01,000 --> 00:00:02,000\n访客: 乙\n\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBadJSONAndMissingTranscriptionFail(t *testing.T) {
	var bad WhisperOutput
	err := json.Unmarshal([]byte(`{"transcription": [}`), &bad)
	if err == nil {
		t.Fatal("expected invalid JSON to fail")
	}

	var missing WhisperOutput
	if err := json.Unmarshal([]byte(`{"result": {}}`), &missing); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if missing.Transcription != nil {
		t.Fatal("missing transcription must stay nil so processFile can reject it")
	}
}

func TestConvertRejectsInvalidOptions(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{Speaker: "A", Tokens: []Token{{Text: "x", Speaker: "A"}}},
	}}

	if _, _, err := Convert(output, Options{SpeakerFormat: "{name}"}); err == nil {
		t.Fatal("expected missing {text} to fail")
	}
	if _, _, err := Convert(output, Options{SpeakerFormat: "{text}", PauseGap: -time.Second}); err == nil {
		t.Fatal("expected negative pause gap to fail")
	}
	if _, _, err := Convert(output, Options{SpeakerFormat: "{text}", MaxChars: -1}); err == nil {
		t.Fatal("expected negative max chars to fail")
	}
	if _, _, err := Convert(output, Options{SpeakerFormat: "{text}", MaxDuration: -time.Second}); err == nil {
		t.Fatal("expected negative max duration to fail")
	}
}

func TestCustomSpeakerFormat(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{
			Speaker: "A",
			Tokens: []Token{
				{Text: "嗨", Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,000"}},
			},
		},
	}}

	options := DefaultOptions()
	options.SpeakerFormat = "[{name}] {text}"
	options.SpeakerNames = map[string]string{"A": "Alice"}
	got, _, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	want := "1\n00:00:00,000 --> 00:00:01,000\n[Alice] 嗨\n\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSpeakerAcceptingNumbers(t *testing.T) {
	var token Token
	if err := json.Unmarshal([]byte(`{"text": "x", "speaker": 2}`), &token); err != nil {
		t.Fatalf("numeric speaker: %v", err)
	}
	if token.Speaker != "2" {
		t.Fatalf("got speaker %q", token.Speaker)
	}

	if err := json.Unmarshal([]byte(`{"text": "x", "speaker": null}`), &token); err != nil {
		t.Fatalf("null speaker: %v", err)
	}
	if token.Speaker != "" {
		t.Fatalf("got speaker %q", token.Speaker)
	}

	if err := json.Unmarshal([]byte(`{"text": "x", "speaker": {"id": 1}}`), &token); err == nil {
		t.Fatal("expected object speaker to fail")
	}
}

func TestProcessFileWritesBOMAndRejectsBadInput(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"transcription":[{"speaker":"A","tokens":[`+
		`{"text":"嗨","timestamps":{"from":"00:00:00,000","to":"00:00:01,000"}}]}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	options := DefaultOptions()
	options.SpeakerNames = map[string]string{"A": "Alice"}
	if err := processFileWithOptions(context.Background(), good, options); err != nil {
		t.Fatalf("processFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "good.srt"))
	if err != nil {
		t.Fatalf("read srt: %v", err)
	}
	if !bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("expected UTF-8 BOM")
	}
	want := "\ufeff1\n00:00:00,000 --> 00:00:01,000\nAlice: 嗨\n\n"
	if string(data) != want {
		t.Fatalf("got %q want %q", string(data), want)
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"transcription": [}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := processFileWithOptions(context.Background(), bad, options); err == nil {
		t.Fatal("expected bad JSON to fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.srt")); !os.IsNotExist(err) {
		t.Fatal("bad JSON must not produce an SRT file")
	}

	missing := filepath.Join(dir, "missing.json")
	if err := os.WriteFile(missing, []byte(`{"result":{}}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := processFileWithOptions(context.Background(), missing, options); err == nil {
		t.Fatal("expected missing transcription to fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.srt")); !os.IsNotExist(err) {
		t.Fatal("missing transcription must not produce an SRT file")
	}
}

func TestSpeakerFormatWithoutNamePlaceholder(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{Speaker: "A", Tokens: []Token{
			{Text: "嗨", Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,000"}},
		}},
	}}
	options := DefaultOptions()
	options.SpeakerFormat = "{text}"
	options.SpeakerNames = map[string]string{"A": "Alice"}
	got, _, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got != "1\n00:00:00,000 --> 00:00:01,000\n嗨\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestSpeakerFormatPlaceholderInNameIsNotExpanded(t *testing.T) {
	output := WhisperOutput{Transcription: []Segment{
		{Speaker: "A", Tokens: []Token{
			{Text: "嗨", Timestamps: Timestamp{From: "00:00:00,000", To: "00:00:01,000"}},
		}},
	}}
	options := DefaultOptions()
	options.SpeakerNames = map[string]string{"A": "{text}"}
	got, _, err := Convert(output, options)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got != "1\n00:00:00,000 --> 00:00:01,000\n{text}: 嗨\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestCuesAreSortedAcrossSegments(t *testing.T) {
	// The second segment starts earlier than the first; output must be ordered
	// by start time and renumbered from 1.
	output := WhisperOutput{Transcription: []Segment{
		{
			Speaker: "B",
			Tokens: []Token{
				{Text: "后", Timestamps: Timestamp{From: "00:00:05,000", To: "00:00:06,000"}},
			},
		},
		{
			Speaker: "A",
			Tokens: []Token{
				{Text: "前", Timestamps: Timestamp{From: "00:00:01,000", To: "00:00:02,000"}},
			},
		},
		{
			Speaker: "C",
			Tokens: []Token{
				{Text: "同", Timestamps: Timestamp{From: "00:00:05,000", To: "00:00:05,500"}},
			},
		},
	}}

	got, _, err := Convert(output, DefaultOptions())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	want := "1\n00:00:01,000 --> 00:00:02,000\nA: 前\n\n" +
		"2\n00:00:05,000 --> 00:00:06,000\nB: 后\n\n" +
		"3\n00:00:05,000 --> 00:00:05,500\nC: 同\n\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
