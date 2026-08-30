package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
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

func Load(name, expectedEngine string) (Config, error) {
	content, err := os.ReadFile(name)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置失败: %w", err)
	}
	var cfg Config
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("配置不是有效的 JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("配置只能包含一个 JSON 对象")
	}
	if err := validate(cfg, expectedEngine); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validate(cfg Config, expectedEngine string) error {
	if cfg.Engine != expectedEngine || (cfg.Engine != "vllm" && cfg.Engine != "sglang") {
		return fmt.Errorf("配置 engine 必须为 %q", expectedEngine)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return errors.New("配置 port 必须在 1 到 65535 之间")
	}
	if cfg.APIPort < 1 || cfg.APIPort > 65535 {
		return errors.New("配置 api_port 必须在 1 到 65535 之间")
	}
	if cfg.Port == cfg.APIPort {
		return errors.New("配置 port 与 api_port 不能相同")
	}
	if !filepath.IsAbs(cfg.ModelPath) || filepath.Clean(cfg.ModelPath) == "/" {
		return errors.New("配置 model_path 必须是具体的宿主机绝对目录")
	}
	if !imagePattern.MatchString(cfg.Image) {
		return errors.New("配置 image 不是有效的容器镜像引用")
	}
	if strings.TrimSpace(cfg.ServedModelName) == "" {
		return errors.New("配置 served_model_name 不能为空")
	}
	if cfg.MaxModelLen <= 0 || cfg.MaxNumSeqs <= 0 || cfg.TensorParallelSize <= 0 {
		return errors.New("max_model_len、max_num_seqs 和 tensor_parallel_size 必须大于 0")
	}
	if cfg.GPUMemoryUtilization <= 0 || cfg.GPUMemoryUtilization > 1 {
		return errors.New("gpu_memory_utilization 必须大于 0 且不超过 1")
	}
	if strings.TrimSpace(cfg.DType) == "" {
		return errors.New("配置 dtype 不能为空")
	}
	for _, arg := range cfg.ExtraArgs {
		if strings.ContainsRune(arg, '\x00') {
			return errors.New("extra_args 不能包含 NUL 字符")
		}
	}
	return nil
}

func WriteEnv(writer io.Writer, cfg Config) error {
	var output strings.Builder
	fmt.Fprintf(&output, "SERVICE_PORT=%d\n", cfg.Port)
	fmt.Fprintf(&output, "API_PORT=%d\n", cfg.APIPort)
	fmt.Fprintf(&output, "MODEL_PATH=%s\n", quoteEnv(cfg.ModelPath))
	fmt.Fprintf(&output, "ENGINE_IMAGE=%s\n", quoteEnv(cfg.Image))
	if _, err := io.WriteString(writer, output.String()); err != nil {
		return fmt.Errorf("输出运行环境失败: %w", err)
	}
	return nil
}

func WriteImage(writer io.Writer, cfg Config) error {
	if _, err := fmt.Fprintln(writer, cfg.Image); err != nil {
		return fmt.Errorf("输出镜像引用失败: %w", err)
	}
	return nil
}

func quoteEnv(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `\'`) + `'`
}
