package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type config struct {
	Port                 int      `json:"port"`
	APIPort              int      `json:"api_port"`
	Engine               string   `json:"engine"`
	Image                string   `json:"image"`
	ModelPath            string   `json:"model_path"`
	ServedModelName      string   `json:"served_model_name"`
	APIKey               string   `json:"api_key"`
	MaxModelLen          int      `json:"max_model_len"`
	GPUMemoryUtilization float64  `json:"gpu_memory_utilization"`
	MaxNumSeqs           int      `json:"max_num_seqs"`
	TensorParallelSize   int      `json:"tensor_parallel_size"`
	DType                string   `json:"dtype"`
	EnablePrefixCaching  bool     `json:"enable_prefix_caching"`
	EnableAutoToolChoice bool     `json:"enable_auto_tool_choice"`
	EnableMetrics        bool     `json:"enable_metrics"`
	ToolCallParser       string   `json:"tool_call_parser"`
	ExtraArgs            []string `json:"extra_args"`
}

var imagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)

func main() {
	engine := flag.String("engine", "", "expected inference engine (vllm or sglang)")
	output := flag.String("output", "env", "output format (env or image)")
	flag.Parse()
	if *engine == "" || flag.NArg() != 1 {
		fatal("用法: dp-inference-config --engine <vllm|sglang> config/config.json")
	}
	cfg, err := readConfig(flag.Arg(0), *engine)
	if err != nil {
		fatal(err.Error())
	}
	if *output == "image" {
		fmt.Println(cfg.Image)
		return
	}
	if *output != "env" {
		fatal("output 必须为 env 或 image")
	}
	fmt.Printf("SERVICE_PORT=%d\n", cfg.Port)
	fmt.Printf("API_PORT=%d\n", cfg.APIPort)
	fmt.Printf("MODEL_PATH=%s\n", quoteEnv(cfg.ModelPath))
	fmt.Printf("ENGINE_IMAGE=%s\n", quoteEnv(cfg.Image))
}

func readConfig(name, expectedEngine string) (config, error) {
	content, err := os.ReadFile(name)
	if err != nil {
		return config{}, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg config
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("配置不是有效的 JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return config{}, errors.New("配置只能包含一个 JSON 对象")
	}
	if cfg.Engine != expectedEngine || (cfg.Engine != "vllm" && cfg.Engine != "sglang") {
		return config{}, fmt.Errorf("配置 engine 必须为 %q", expectedEngine)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return config{}, errors.New("配置 port 必须在 1 到 65535 之间")
	}
	if cfg.APIPort < 1 || cfg.APIPort > 65535 {
		return config{}, errors.New("配置 api_port 必须在 1 到 65535 之间")
	}
	if cfg.Port == cfg.APIPort {
		return config{}, errors.New("配置 port 与 api_port 不能相同")
	}
	if !filepath.IsAbs(cfg.ModelPath) || filepath.Clean(cfg.ModelPath) == "/" {
		return config{}, errors.New("配置 model_path 必须是具体的宿主机绝对目录")
	}
	if !imagePattern.MatchString(cfg.Image) {
		return config{}, errors.New("配置 image 不是有效的容器镜像引用")
	}
	if strings.TrimSpace(cfg.ServedModelName) == "" {
		return config{}, errors.New("配置 served_model_name 不能为空")
	}
	if cfg.MaxModelLen <= 0 || cfg.MaxNumSeqs <= 0 || cfg.TensorParallelSize <= 0 {
		return config{}, errors.New("max_model_len、max_num_seqs 和 tensor_parallel_size 必须大于 0")
	}
	if cfg.GPUMemoryUtilization <= 0 || cfg.GPUMemoryUtilization > 1 {
		return config{}, errors.New("gpu_memory_utilization 必须大于 0 且不超过 1")
	}
	if strings.TrimSpace(cfg.DType) == "" {
		return config{}, errors.New("配置 dtype 不能为空")
	}
	for _, arg := range cfg.ExtraArgs {
		if strings.ContainsRune(arg, '\x00') {
			return config{}, errors.New("extra_args 不能包含 NUL 字符")
		}
	}
	return cfg, nil
}

func quoteEnv(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return `"` + replacer.Replace(value) + `"`
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
