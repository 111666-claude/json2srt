package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --- Struct Definitions ---

// Timestamp represents a timestamp structure containing start and end times
type Timestamp struct {
	From string `json:"from"` // Start time, format "00:00:00,000"
	To   string `json:"to"`   // End time, format "00:00:01,960"
}

// Offset holds millisecond offsets. Some Whisper/whisper.cpp outputs provide
// these even when the formatted timestamps are absent.
type Offset struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// SpeakerID is a speaker marker. It accepts both strings ("SPEAKER_00") and
// JSON numbers (0, 1, ...) so that diarization tools using either convention
// can feed json2srt unchanged.
type SpeakerID string

// UnmarshalJSON implements json.Unmarshaler.
func (s *SpeakerID) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*s = ""
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		*s = SpeakerID(value)
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return fmt.Errorf("speaker must be a string or number: %v", err)
	}
	*s = SpeakerID(number.String())
	return nil
}

// Token represents a single word in transcription
type Token struct {
	Text       string    `json:"text"`              // Token text
	Timestamps Timestamp `json:"timestamps"`        // Timestamps for this token
	Offsets    *Offset   `json:"offsets,omitempty"` // Optional millisecond fallback
	Speaker    SpeakerID `json:"speaker,omitempty"` // Optional per-word speaker
}

// Segment represents a paragraph in transcription
type Segment struct {
	Timestamps Timestamp `json:"timestamps"`        // Timestamps for the paragraph
	Offsets    *Offset   `json:"offsets,omitempty"` // Optional millisecond fallback
	Text       string    `json:"text"`              // Text content of the paragraph
	Speaker    SpeakerID `json:"speaker,omitempty"` // Default speaker for this segment
	Tokens     []Token   `json:"tokens"`            // Token list for this paragraph
}

// WhisperOutput represents the root structure of Whisper transcription output
type WhisperOutput struct {
	Transcription []Segment `json:"transcription"` // List of transcription paragraphs
}

// --- Options ---

// Options controls speaker-aware subtitle generation. It is ignored entirely
// when the input contains no speaker markers, which keeps legacy output
// byte-for-byte identical.
type Options struct {
	// SpeakerNames maps speaker markers to display names.
	SpeakerNames map[string]string
	// SpeakerFormat is applied to every speaker-aware cue. It supports the
	// placeholders {name} and {text}; {text} is required.
	SpeakerFormat string
	// DefaultSpeaker labels words that have no speaker marker while other
	// parts of the same file do.
	DefaultSpeaker string
	// PauseGap splits a cue when the silence before the next word exceeds it.
	// Zero disables pause-based splitting.
	PauseGap time.Duration
	// MaxChars limits the rune count of the spoken text per cue. Zero disables
	// the limit. A single word longer than the limit is kept intact.
	MaxChars int
	// MaxDuration limits a cue's duration. Zero disables the limit. A single
	// word longer than the limit is kept intact.
	MaxDuration time.Duration
}

// DefaultOptions returns the defaults used by the command line tool.
func DefaultOptions() Options {
	return Options{
		SpeakerFormat:  "{name}: {text}",
		DefaultSpeaker: "Speaker",
		PauseGap:       800 * time.Millisecond,
		MaxChars:       42,
		MaxDuration:    7 * time.Second,
	}
}

func (o Options) withDefaults() Options {
	if strings.TrimSpace(o.SpeakerFormat) == "" {
		o.SpeakerFormat = "{name}: {text}"
	}
	if strings.TrimSpace(o.DefaultSpeaker) == "" {
		o.DefaultSpeaker = "Speaker"
	}
	return o
}

// Validate checks option values.
func (o Options) Validate() error {
	if o.PauseGap < 0 {
		return fmt.Errorf("pause gap must not be negative")
	}
	if o.MaxChars < 0 {
		return fmt.Errorf("max chars must not be negative")
	}
	if o.MaxDuration < 0 {
		return fmt.Errorf("max duration must not be negative")
	}
	if !strings.Contains(o.SpeakerFormat, "{text}") {
		return fmt.Errorf("speaker format %q must contain {text}", o.SpeakerFormat)
	}
	return nil
}

// --- Legacy logic (must remain unchanged) ---

// extractTimestamps extracts start and end timestamps from segment's tokens
// Returns startTime, endTime, hasValidTokens
func extractTimestamps(segment Segment) (string, string, bool) {
	var startTime, endTime string
	hasValidTokens := false

	// Find first non-noise token's start time
	for _, token := range segment.Tokens {
		if !strings.HasPrefix(token.Text, "[_") && token.Text != "" {
			// Use token's From as start time
			startTime = token.Timestamps.From
			hasValidTokens = true
			break
		}
	}

	// Find the last non-noise token's end time
	for j := len(segment.Tokens) - 1; j >= 0; j-- {
		token := segment.Tokens[j]
		if !strings.HasPrefix(token.Text, "[_") && token.Text != "" {
			endTime = token.Timestamps.To
			hasValidTokens = true
			break
		}
	}

	// If there are no valid tokens, fall back to the segment timestamps
	if !hasValidTokens || startTime == "" || endTime == "" {
		startTime = segment.Timestamps.From
		endTime = segment.Timestamps.To
	}

	return startTime, endTime, hasValidTokens
}

// renderLegacySRT reproduces the original per-segment output exactly.
func renderLegacySRT(output WhisperOutput) string {
	var srtBuilder strings.Builder

	for i, segment := range output.Transcription {
		startTime, endTime, _ := extractTimestamps(segment)
		if startTime == "" || endTime == "" {
			continue // skip invalid segment
		}

		text := strings.TrimSpace(segment.Text)
		srtBuilder.WriteString(fmt.Sprintf("%d\n", i+1))
		srtBuilder.WriteString(fmt.Sprintf("%s --> %s\n", startTime, endTime))
		srtBuilder.WriteString(fmt.Sprintf("%s\n\n", text))
	}

	return srtBuilder.String()
}

// --- Speaker-aware conversion ---

// Convert renders an SRT body (without a BOM). When the input has no speaker
// markers the legacy renderer is used and no warnings are produced.
func Convert(output WhisperOutput, options Options) (string, []string, error) {
	options = options.withDefaults()
	if err := options.Validate(); err != nil {
		return "", nil, err
	}

	if !hasSpeakerLabels(output) {
		return renderLegacySRT(output), nil, nil
	}

	warnings := &warningSet{seen: make(map[string]bool)}
	cues := buildSpeakerCues(output, options, warnings)
	return renderCues(cues, options, warnings), warnings.list, nil
}

func hasSpeakerLabels(output WhisperOutput) bool {
	for _, segment := range output.Transcription {
		if strings.TrimSpace(string(segment.Speaker)) != "" {
			return true
		}
		for _, token := range segment.Tokens {
			if strings.TrimSpace(string(token.Speaker)) != "" {
				return true
			}
		}
	}
	return false
}

// word is an internal, timestamp-normalized word.
type word struct {
	text         string
	start        int64
	end          int64
	startSet     bool
	endSet       bool
	speaker      string
	segmentIndex int

	segStart    int64
	segEnd      int64
	segStartSet bool
	segEndSet   bool
}

// cue is a subtitle unit before rendering.
type cue struct {
	start   int64
	end     int64
	speaker string
	text    string
}

// parseTimecode parses "HH:MM:SS,mmm" or "HH:MM:SS.mmm". Fractional digits
// may be 0-3 long; shorter fractions are scaled to milliseconds.
func parseTimecode(raw string) (int64, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, false
	}

	separator := strings.LastIndexAny(value, ",.")
	if separator < 0 {
		return 0, false
	}

	base := value[:separator]
	fraction := value[separator+1:]
	if fraction == "" || len(fraction) > 3 {
		return 0, false
	}
	for _, r := range fraction {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	millis, _ := strconv.Atoi(fraction)
	for i := len(fraction); i < 3; i++ {
		millis *= 10
	}

	parts := strings.Split(base, ":")
	if len(parts) != 3 {
		return 0, false
	}
	values := make([]int, 3)
	for i, part := range parts {
		if part == "" {
			return 0, false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, false
			}
		}
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return 0, false
		}
		values[i] = parsed
	}
	if values[1] > 59 || values[2] > 59 {
		return 0, false
	}

	return int64(values[0])*3600000 + int64(values[1])*60000 + int64(values[2])*1000 + int64(millis), true
}

// formatTimecode renders milliseconds as SRT's "HH:MM:SS,mmm".
func formatTimecode(millis int64) string {
	if millis < 0 {
		millis = 0
	}
	hours := millis / 3600000
	minutes := (millis % 3600000) / 60000
	seconds := (millis % 60000) / 1000
	remainder := millis % 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, remainder)
}

func segmentTimeRange(segment Segment) (int64, int64, bool, bool) {
	start, startOK := parseTimecode(segment.Timestamps.From)
	end, endOK := parseTimecode(segment.Timestamps.To)
	if !startOK && segment.Offsets != nil {
		start = segment.Offsets.From
		startOK = true
	}
	if !endOK && segment.Offsets != nil {
		end = segment.Offsets.To
		endOK = true
	}
	return start, end, startOK, endOK
}

func tokenTimeRange(token Token) (int64, int64, bool, bool) {
	start, startOK := parseTimecode(token.Timestamps.From)
	end, endOK := parseTimecode(token.Timestamps.To)
	if !startOK && token.Offsets != nil {
		start = token.Offsets.From
		startOK = true
	}
	if !endOK && token.Offsets != nil {
		end = token.Offsets.To
		endOK = true
	}
	return start, end, startOK, endOK
}

func buildSpeakerCues(output WhisperOutput, options Options, warnings *warningSet) []cue {
	var cues []cue

	for segmentIndex, segment := range output.Transcription {
		segmentStart, segmentEnd, segmentStartOK, segmentEndOK := segmentTimeRange(segment)
		segmentSpeaker := strings.TrimSpace(string(segment.Speaker))

		words := make([]word, 0, len(segment.Tokens))
		for _, token := range segment.Tokens {
			text := token.Text
			trimmed := strings.TrimSpace(text)
			// Noise markers and empty words carry no subtitle text.
			if trimmed == "" || strings.HasPrefix(trimmed, "[_") {
				continue
			}

			speaker := strings.TrimSpace(string(token.Speaker))
			if speaker == "" {
				speaker = segmentSpeaker
			}

			start, end, startOK, endOK := tokenTimeRange(token)
			words = append(words, word{
				text:         text,
				start:        start,
				end:          end,
				startSet:     startOK,
				endSet:       endOK,
				speaker:      speaker,
				segmentIndex: segmentIndex,
				segStart:     segmentStart,
				segEnd:       segmentEnd,
				segStartSet:  segmentStartOK,
				segEndSet:    segmentEndOK,
			})
		}

		// If a speaker-marked segment has no usable words, fall back to its
		// segment text once, using the segment timestamps.
		if len(words) == 0 {
			text := strings.TrimSpace(segment.Text)
			if text == "" {
				continue
			}
			warnings.add("segment %d: no usable word tokens; using segment text", segmentIndex+1)
			words = append(words, word{
				text:         text,
				start:        segmentStart,
				end:          segmentEnd,
				startSet:     segmentStartOK,
				endSet:       segmentEndOK,
				speaker:      segmentSpeaker,
				segmentIndex: segmentIndex,
				segStart:     segmentStart,
				segEnd:       segmentEnd,
				segStartSet:  segmentStartOK,
				segEndSet:    segmentEndOK,
			})
		}

		words = fillWordTimes(words, warnings)
		cues = append(cues, buildSegmentCues(words, options)...)
	}

	return cues
}

// fillWordTimes infers missing word boundaries in a single pass and drops
// words that cannot be placed at all.
func fillWordTimes(words []word, warnings *warningSet) []word {
	if len(words) == 0 {
		return words
	}

	// nextStart[i] is the first usable start time at or after i.
	nextStart := make([]int64, len(words))
	nextSet := make([]bool, len(words))
	for i := len(words) - 1; i >= 0; i-- {
		switch {
		case words[i].startSet:
			nextStart[i] = words[i].start
			nextSet[i] = true
		case i+1 < len(words):
			nextStart[i] = nextStart[i+1]
			nextSet[i] = nextSet[i+1]
		}
	}

	result := make([]word, 0, len(words))
	var previousEnd int64
	previousSet := false

	for i, current := range words {
		switch {
		case current.startSet && current.endSet:
			// Complete.
		case current.startSet:
			if i+1 < len(words) && nextSet[i+1] && nextStart[i+1] >= current.start {
				current.end = nextStart[i+1]
			} else if current.segEndSet && current.segEnd >= current.start {
				current.end = current.segEnd
			} else {
				current.end = current.start
				warnings.add("segment %d: word %q has no end time; using its start",
					current.segmentIndex+1, displayWord(current.text))
			}
			current.endSet = true
		case current.endSet:
			if previousSet && previousEnd <= current.end {
				current.start = previousEnd
			} else if current.segStartSet && current.segStart <= current.end {
				current.start = current.segStart
			} else {
				current.start = current.end
				warnings.add("segment %d: word %q has no start time; using its end",
					current.segmentIndex+1, displayWord(current.text))
			}
			current.startSet = true
		default:
			hasNext := i+1 < len(words) && nextSet[i+1]
			switch {
			case previousSet && hasNext:
				// Bridge the gap between neighbours; degenerate data collapses
				// to a zero-length word instead of duplicating a whole segment.
				current.start = previousEnd
				current.end = nextStart[i+1]
				if current.end < current.start {
					current.end = current.start
				}
				current.startSet = true
				current.endSet = true
			case previousSet:
				current.start = previousEnd
				current.end = previousEnd
				current.startSet = true
				current.endSet = true
			case hasNext:
				if current.segStartSet && current.segStart <= nextStart[i+1] {
					current.start = current.segStart
				} else {
					current.start = nextStart[i+1]
				}
				current.end = nextStart[i+1]
				current.startSet = true
				current.endSet = true
			case current.segStartSet && current.segEndSet && current.segStart <= current.segEnd:
				current.start = current.segStart
				current.end = current.segEnd
				current.startSet = true
				current.endSet = true
			default:
				warnings.add("segment %d: word %q skipped: no usable timestamps",
					current.segmentIndex+1, displayWord(current.text))
				continue
			}
		}

		if current.start < 0 {
			current.start = 0
		}
		if current.end < current.start {
			current.end = current.start
			warnings.add("segment %d: word %q ends before it starts; using start as end",
				current.segmentIndex+1, displayWord(current.text))
		}

		previousEnd = current.end
		previousSet = true
		result = append(result, current)
	}

	return result
}

// buildSegmentCues splits one segment by speaker, pause and configured limits.
func buildSegmentCues(words []word, options Options) []cue {
	var cues []cue
	var current *cue
	var raw strings.Builder
	var previousEnd int64
	previousSet := false

	finalize := func() {
		if current == nil {
			return
		}
		current.text = strings.TrimSpace(raw.String())
		if current.text != "" {
			cues = append(cues, *current)
		}
		current = nil
		raw.Reset()
	}

	startCue := func(w word) {
		current = &cue{
			start:   w.start,
			end:     w.end,
			speaker: w.speaker,
		}
		raw.WriteString(w.text)
	}

	for _, w := range words {
		if current == nil {
			startCue(w)
			previousEnd = w.end
			previousSet = true
			continue
		}

		shouldSplit := false

		if w.speaker != current.speaker {
			shouldSplit = true
		}

		if !shouldSplit && options.PauseGap > 0 && previousSet {
			gap := time.Duration(w.start-previousEnd) * time.Millisecond
			if gap > options.PauseGap {
				shouldSplit = true
			}
		}

		prospectiveText := strings.TrimSpace(raw.String() + w.text)
		if !shouldSplit && options.MaxChars > 0 && len([]rune(prospectiveText)) > options.MaxChars {
			shouldSplit = true
		}

		if !shouldSplit && options.MaxDuration > 0 {
			start, end := current.start, current.end
			if w.start < start {
				start = w.start
			}
			if w.end > end {
				end = w.end
			}
			if time.Duration(end-start)*time.Millisecond > options.MaxDuration {
				shouldSplit = true
			}
		}

		if shouldSplit {
			finalize()
			startCue(w)
		} else {
			raw.WriteString(w.text)
			if w.start < current.start {
				current.start = w.start
			}
			if w.end > current.end {
				current.end = w.end
			}
		}

		previousEnd = w.end
		previousSet = true
	}

	finalize()
	return cues
}

// renderCues sorts cues by start time (stable for simultaneous starts) and
// renders the SRT body. Overlapping cues keep their original timecodes.
func renderCues(cues []cue, options Options, warnings *warningSet) string {
	sort.SliceStable(cues, func(i, j int) bool {
		return cues[i].start < cues[j].start
	})

	var builder strings.Builder
	for i, item := range cues {
		name := speakerLabel(item.speaker, options, warnings)
		// A single-pass replacer keeps placeholder-looking text inside an
		// inserted name or cue from being substituted again.
		text := strings.NewReplacer("{name}", name, "{text}", item.text).Replace(options.SpeakerFormat)

		builder.WriteString(fmt.Sprintf("%d\n", i+1))
		builder.WriteString(fmt.Sprintf("%s --> %s\n", formatTimecode(item.start), formatTimecode(item.end)))
		builder.WriteString(fmt.Sprintf("%s\n\n", text))
	}
	return builder.String()
}

func speakerLabel(speaker string, options Options, warnings *warningSet) string {
	speaker = strings.TrimSpace(speaker)
	if speaker == "" {
		return options.DefaultSpeaker
	}
	if name, ok := options.SpeakerNames[speaker]; ok {
		if strings.TrimSpace(name) != "" {
			return name
		}
		return speaker
	}
	// Only warn when a name mapping was requested; without mappings the raw
	// speaker ID is the expected output rather than an error.
	if len(options.SpeakerNames) > 0 {
		warnings.add("unknown speaker %q: using raw speaker ID as label", speaker)
	}
	return speaker
}

func displayWord(text string) string {
	trimmed := strings.TrimSpace(text)
	runes := []rune(trimmed)
	if len(runes) > 20 {
		return string(runes[:20]) + "..."
	}
	return trimmed
}

// warningSet keeps warnings unique while preserving discovery order.
type warningSet struct {
	seen map[string]bool
	list []string
}

func (w *warningSet) add(format string, args ...interface{}) {
	message := fmt.Sprintf(format, args...)
	if w.seen[message] {
		return
	}
	w.seen[message] = true
	w.list = append(w.list, message)
}
