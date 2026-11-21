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

## 说话人字幕

没有 `speaker` 字段的文件保持原有输出（每个 Whisper 段一条字幕）。只要任一
segment 或 token 带 `speaker` 标记，json2srt 就切换到说话人模式：

- 按说话人变化、静音停顿和字幕上限切分；
- 每条字幕前加可配置的姓名标签；
- 重叠发言各自成条、保留各自时码，按开始时间稳定排序；
- token 级 `speaker` 优先于 segment 级 `speaker`。

输入格式：在 segment 或 `tokens` 中单个 token 上加 `"speaker": "SPEAKER_00"`，
也接受数字 ID（`"speaker": 0`）。

```bash
./json2srt \
  -speaker SPEAKER_00=Alice \
  -speaker SPEAKER_01=Bob \
  -speaker-format '{name}: {text}' \
  -pause-gap 800ms \
  -max-chars 42 \
  -max-duration 7s
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-speaker ID=NAME` | 无 | 说话人 ID 映射为姓名，可重复 |
| `-speaker-format` | `{name}: {text}` | 字幕模板，必须包含 `{text}` |
| `-default-speaker` | `Speaker` | 有标记文件中未标记词的默认标签 |
| `-pause-gap` | `800ms` | 静音超过该值则切分（`0` 关闭） |
| `-max-chars` | `42` | 单条字幕最大字符数（`0` 关闭） |
| `-max-duration` | `7s` | 单条字幕最大时长（`0` 关闭） |

边界情况：

- **缺词时间**：从相邻词或 segment 时间范围推断，完全无法定位的词跳过并告警。
- **未知 speaker**：用原始 ID 作标签；配置了姓名映射时额外告警。
- **空词与噪音标记**（`[_BEG_]`、`[_TT_*]`）：直接丢弃。
- **交错/重叠发言**：保留各自时码，按开始时间稳定排序。
- **坏 JSON 或缺 `transcription`**：记录错误，不生成 SRT 文件。

## 测试

```bash
go test ./...
```

`testdata/overlap_two_speakers.json` 是固定的两人重叠发言 Whisper 夹具；
`convert_test.go` 核对字幕文本、序号、时码以及无标记时的原输出。

## Dependencies

- Go 1.16+

## Output

- Each JSON file generates corresponding SRT file with BOM
- SRT file timestamps are precise to each word's start and end time

## License

MIT
