package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// stringList collects repeatable string flags.
type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

// --- Core Logic ---

// processFile 处理单个 JSON 文件，生成 SRT 文件
// ctx 用于支持取消操作
func processFile(ctx context.Context, jsonPath string) error {
	return processFileWithOptions(ctx, jsonPath, DefaultOptions())
}

// processFileWithOptions is processFile with explicit conversion options.
func processFileWithOptions(ctx context.Context, jsonPath string, options Options) error {
	// 检查 context 是否已取消
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	srtPath := strings.TrimSuffix(jsonPath, filepath.Ext(jsonPath)) + ".srt"
	byteValue, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("读取文件 %s 失败: %v", filepath.Base(jsonPath), err)
	}
	var output WhisperOutput
	if err := json.Unmarshal(byteValue, &output); err != nil {
		return fmt.Errorf("解析 JSON 文件 %s 失败: %v", filepath.Base(jsonPath), err)
	}

	// 验证 JSON 结构完整性
	if output.Transcription == nil {
		return fmt.Errorf("JSON 文件 %s 缺少 transcription 字段", filepath.Base(jsonPath))
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	srtBody, warnings, err := Convert(output, options)
	if err != nil {
		return fmt.Errorf("生成字幕 %s 失败: %v", filepath.Base(jsonPath), err)
	}
	for _, warning := range warnings {
		log.Printf("警告 %s: %s", filepath.Base(jsonPath), warning)
	}

	bom := []byte{0xEF, 0xBB, 0xBF}
	content := []byte(srtBody)
	dataToWrite := append(bom, content...)
	if err := os.WriteFile(srtPath, dataToWrite, 0644); err != nil {
		return fmt.Errorf("写入 SRT 文件 %s 失败: %v", filepath.Base(srtPath), err)
	}
	fmt.Printf("    [成功] 已生成 SRT 文件: %s\n", filepath.Base(srtPath))
	return nil
}

// worker 处理文件的 goroutine
func worker(ctx context.Context, id int, jobs <-chan string, wg *sync.WaitGroup, options Options) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case jsonPath, ok := <-jobs:
			if !ok {
				return
			}
			fmt.Printf("Worker %d 开始处理文件: %s\n", id, filepath.Base(jsonPath))
			if err := processFileWithOptions(ctx, jsonPath, options); err != nil {
				log.Printf("Worker %d 处理文件 %s 时出错: %v", id, filepath.Base(jsonPath), err)
			}
		}
	}
}

// parseSpeakerNames parses repeated --speaker ID=NAME flags.
func parseSpeakerNames(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	names := make(map[string]string, len(values))
	for _, value := range values {
		separator := strings.Index(value, "=")
		if separator < 0 {
			return nil, fmt.Errorf("invalid --speaker value %q: expected ID=NAME", value)
		}
		id := strings.TrimSpace(value[:separator])
		name := strings.TrimSpace(value[separator+1:])
		if id == "" || name == "" {
			return nil, fmt.Errorf("invalid --speaker value %q: expected ID=NAME", value)
		}
		if _, exists := names[id]; exists {
			return nil, fmt.Errorf("duplicate --speaker mapping for %q", id)
		}
		names[id] = name
	}
	return names, nil
}

func main() {
	ctx := context.Background() // 创建根 context，可用于取消操作

	options := DefaultOptions()
	var speakerFlags stringList
	flag.Var(&speakerFlags, "speaker", "map a speaker ID to a display name, repeatable (e.g. SPEAKER_00=Alice)")
	flag.StringVar(&options.SpeakerFormat, "speaker-format", options.SpeakerFormat, "speaker cue template; supports {name} and requires {text}")
	flag.StringVar(&options.DefaultSpeaker, "default-speaker", options.DefaultSpeaker, "label for words without a speaker marker")
	flag.DurationVar(&options.PauseGap, "pause-gap", options.PauseGap, "split cues on silences longer than this (0 disables)")
	flag.IntVar(&options.MaxChars, "max-chars", options.MaxChars, "maximum spoken characters per cue (0 disables)")
	flag.DurationVar(&options.MaxDuration, "max-duration", options.MaxDuration, "maximum cue duration (0 disables)")
	flag.Parse()

	names, err := parseSpeakerNames(speakerFlags)
	if err != nil {
		log.Fatalf("%v", err)
	}
	options.SpeakerNames = names
	if err := options.Validate(); err != nil {
		log.Fatalf("参数错误: %v", err)
	}

	// --- MODIFIED: 使用 os.Getwd() 获取当前终端的工作目录 ---
	workDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("获取当前工作目录失败: %v", err)
	}

	fmt.Printf("程序正在当前终端目录中运行: %s\n", workDir)

	// MODIFIED: 使用 workDir 变量
	files, err := os.ReadDir(workDir)
	if err != nil {
		log.Fatalf("读取目录失败: %v", err)
	}

	jsonPaths := []string{}
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(strings.ToLower(file.Name()), ".json") {
			// MODIFIED: 使用 workDir 变量
			jsonPaths = append(jsonPaths, filepath.Join(workDir, file.Name()))
		}
	}

	if len(jsonPaths) == 0 {
		fmt.Println("任务完成：未在当前目录中找到任何 .json 文件。")
		return
	}

	numJobs := len(jsonPaths)
	jobs := make(chan string, numJobs)
	var wg sync.WaitGroup

	numWorkers := runtime.NumCPU()
	fmt.Printf("启动 %d 个 Worker 并行处理 %d 个文件...\n", numWorkers, numJobs)
	fmt.Println(strings.Repeat("=", 40))

	for w := 1; w <= numWorkers; w++ {
		wg.Add(1)
		go worker(ctx, w, jobs, &wg, options)
	}

	for _, path := range jsonPaths {
		jobs <- path
	}
	close(jobs)

	wg.Wait()

	fmt.Println(strings.Repeat("=", 40))
	fmt.Printf("任务完成：共处理了 %d 个 JSON 文件。\n", numJobs)
}
