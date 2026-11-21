# json2srt

Convert OpenAI Whisper JSON output with word-level timestamps to precise SRT subtitle files.

## Purpose

OpenAI Whisper generates JSON files with word-level timestamps when using `--word_timestamps True`. This tool converts those JSON files to SRT subtitle format, **preserving per-character/word timing accuracy** rather than sentence-level timestamps.

This is particularly useful for scenarios requiring precise synchronization:
- Language learning applications
- Precise subtitle production
- Audio analysis and research

## Usage

1. Generate JSON file with word-level timestamps using Whisper:
   ```bash
   whisper audio.mp3 --word_timestamps True --output_format json
   ```
2. Place generated JSON file in same directory as program
3. Run program to auto-convert to precise SRT files

## Features

- ✨ **Precise timestamps**: Maintains Whisper's word-level timing precision
- 🚀 **Batch processing**: Auto-processes all JSON files in directory
- ⚡ **Concurrent processing**: Uses multi-core CPU for faster processing
- 📝 **BOM support**: Generates SRT files with BOM for correct Chinese character display
- 🔍 **Smart filtering**: Auto-skips Whisper noise markers (like `[_TT_123]`)

## Run

```bash
go run .
```
Or compile and run:
```bash
go build -o json2srt .
./json2srt
```

## Speaker-labelled subtitles

Files with no `speaker` field are converted exactly as before (one cue per
Whisper segment). When any segment or word carries a `speaker` marker, json2srt
switches to speaker-aware cues:

- words are grouped into cues by speaker change, silent pauses and cue limits;
- every cue is prefixed with a configurable name label;
- overlapping speech produces separate cues that keep their own timecodes,
  ordered by start time (stable for equal starts);
- per-word `speaker` overrides the segment-level `speaker`.

Input format: add `"speaker": "SPEAKER_00"` to a segment or to individual
tokens inside `"tokens"`. Numeric IDs (`"speaker": 0`) are accepted too.

```bash
./json2srt \
  -speaker SPEAKER_00=Alice \
  -speaker SPEAKER_01=Bob \
  -speaker-format '{name}: {text}' \
  -pause-gap 800ms \
  -max-chars 42 \
  -max-duration 7s
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `-speaker ID=NAME` | none | Map a speaker ID to a display name; repeatable |
| `-speaker-format` | `{name}: {text}` | Cue template; must contain `{text}` |
| `-default-speaker` | `Speaker` | Label for unlabelled words in a labelled file |
| `-pause-gap` | `800ms` | Split cues on silences longer than this (`0` disables) |
| `-max-chars` | `42` | Max spoken characters per cue (`0` disables) |
| `-max-duration` | `7s` | Max cue duration (`0` disables) |

Edge cases:

- **Missing word times** are inferred from neighbours or the segment range; a
  word that cannot be placed at all is skipped with a warning.
- **Unknown speaker IDs** fall back to the raw ID as the label, with a warning
  when name mappings were supplied.
- **Empty words and noise markers** (`[_BEG_]`, `[_TT_*]`) are dropped.
- **Interleaved/overlapping speech** keeps every cue's own timecodes; cues are
  stably sorted by start time.
- **Invalid JSON or a missing `transcription` field** logs an error and writes
  no SRT file.

## Dependencies

- Go 1.16+

## Output

- Each JSON file generates corresponding SRT file with BOM
- SRT file timestamps are precise to each word's start and end time
- With speaker markers, each cue carries the speaker label chosen above

## Test

```bash
go test ./...
```

`testdata/overlap_two_speakers.json` is a fixed two-speaker Whisper fixture with
overlapping and interleaved speech; `convert_test.go` checks cue text, numbering,
timecodes and unchanged legacy output.

## License

MIT
