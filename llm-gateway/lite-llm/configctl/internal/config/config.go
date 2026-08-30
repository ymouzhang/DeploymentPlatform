package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
)

type Config struct {
	Port                   int      `json:"port"`
	APIPort                int      `json:"api_port"`
	LiteLLMImage           string   `json:"litellm_image"`
	PostgresImage          string   `json:"postgres_image"`
	MasterKey              string   `json:"master_key"`
	SaltKey                string   `json:"salt_key"`
	Database               Database `json:"database"`
	StoreModelInDB         bool     `json:"store_model_in_db"`
	NumWorkers             int      `json:"num_workers"`
	RequestTimeout         int      `json:"request_timeout_seconds"`
	DatabasePoolLimit      int      `json:"database_pool_limit"`
	ProxyBatchWriteSeconds int      `json:"proxy_batch_write_seconds"`
	Models                 []Model  `json:"models"`
}

type Database struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
}

type Model struct {
	ModelName string `json:"model_name"`
	Model     string `json:"model"`
	APIBase   string `json:"api_base"`
	APIKey    string `json:"api_key"`
}

type liteLLMConfig struct {
	ModelList       []liteLLMModel         `json:"model_list"`
	GeneralSettings liteLLMGeneralSettings `json:"general_settings"`
	LiteLLMSettings liteLLMSettings        `json:"litellm_settings"`
}

type liteLLMModel struct {
	ModelName     string        `json:"model_name"`
	LiteLLMParams liteLLMParams `json:"litellm_params"`
}

type liteLLMParams struct {
	Model   string `json:"model"`
	APIBase string `json:"api_base"`
	APIKey  string `json:"api_key"`
}

type liteLLMGeneralSettings struct {
	StoreModelInDB              bool `json:"store_model_in_db"`
	DatabaseConnectionPoolLimit int  `json:"database_connection_pool_limit"`
	ProxyBatchWriteAt           int  `json:"proxy_batch_write_at"`
}

type liteLLMSettings struct {
	RequestTimeout int  `json:"request_timeout"`
	JSONLogs       bool `json:"json_logs"`
	SetVerbose     bool `json:"set_verbose"`
}

var (
	imagePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)
	databaseIDPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
)

func Load(name string, requireSecrets bool) (Config, error) {
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
	if err := validate(cfg, requireSecrets); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validate(cfg Config, requireSecrets bool) error {
	if cfg.Port < 1 || cfg.Port > 65535 || cfg.APIPort < 1 || cfg.APIPort > 65535 {
		return errors.New("port 和 api_port 必须在 1 到 65535 之间")
	}
	if cfg.Port == cfg.APIPort {
		return errors.New("port 与 api_port 不能相同")
	}
	if !imagePattern.MatchString(cfg.LiteLLMImage) || !imagePattern.MatchString(cfg.PostgresImage) {
		return errors.New("litellm_image 或 postgres_image 不是有效的容器镜像引用")
	}
	if !strings.HasPrefix(cfg.MasterKey, "sk-") || len(cfg.MasterKey) < 16 {
		return errors.New("master_key 必须以 sk- 开头且长度至少为 16")
	}
	if len(cfg.SaltKey) < 16 {
		return errors.New("salt_key 长度至少为 16")
	}
	if !databaseIDPattern.MatchString(cfg.Database.Name) || !databaseIDPattern.MatchString(cfg.Database.User) {
		return errors.New("database.name 和 database.user 只能包含字母、数字、下划线和连字符")
	}
	if len(cfg.Database.Password) < 16 || strings.ContainsAny(cfg.Database.Password, "\r\n\x00") {
		return errors.New("database.password 长度至少为 16 且不能包含换行或 NUL")
	}
	if cfg.NumWorkers < 1 || cfg.NumWorkers > 64 {
		return errors.New("num_workers 必须在 1 到 64 之间")
	}
	if cfg.RequestTimeout < 1 || cfg.DatabasePoolLimit < 1 || cfg.ProxyBatchWriteSeconds < 1 {
		return errors.New("request_timeout_seconds、database_pool_limit 和 proxy_batch_write_seconds 必须大于 0")
	}
	if len(cfg.Models) == 0 && !cfg.StoreModelInDB {
		return errors.New("未启用数据库模型管理时 models 不能为空")
	}
	seen := make(map[string]struct{}, len(cfg.Models))
	for index, model := range cfg.Models {
		if strings.TrimSpace(model.ModelName) == "" || strings.TrimSpace(model.Model) == "" {
			return fmt.Errorf("models[%d] 的 model_name 和 model 不能为空", index)
		}
		if _, exists := seen[model.ModelName]; exists {
			return fmt.Errorf("models 中存在重复的 model_name：%s", model.ModelName)
		}
		seen[model.ModelName] = struct{}{}
		parsed, err := url.Parse(model.APIBase)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return fmt.Errorf("models[%d].api_base 必须是有效的 http/https 地址", index)
		}
		if strings.ContainsAny(model.APIKey, "\r\n\x00") {
			return fmt.Errorf("models[%d].api_key 不能包含换行或 NUL", index)
		}
	}
	if requireSecrets {
		secrets := []struct{ name, value string }{
			{"master_key", cfg.MasterKey}, {"salt_key", cfg.SaltKey}, {"database.password", cfg.Database.Password},
		}
		for _, secret := range secrets {
			if isPlaceholder(secret.value) {
				return fmt.Errorf("%s 仍是占位值，请先修改", secret.name)
			}
		}
		for index, model := range cfg.Models {
			if isPlaceholder(model.APIKey) {
				return fmt.Errorf("models[%d].api_key 仍是占位值，请先修改", index)
			}
		}
	}
	return nil
}

func WriteEnv(writer io.Writer, cfg Config) error {
	var output strings.Builder
	fmt.Fprintf(&output, "SERVICE_PORT=%d\n", cfg.Port)
	fmt.Fprintf(&output, "API_PORT=%d\n", cfg.APIPort)
	fmt.Fprintf(&output, "LITELLM_IMAGE=%s\n", quoteEnv(cfg.LiteLLMImage))
	fmt.Fprintf(&output, "POSTGRES_IMAGE=%s\n", quoteEnv(cfg.PostgresImage))
	fmt.Fprintf(&output, "LITELLM_MASTER_KEY=%s\n", quoteEnv(cfg.MasterKey))
	fmt.Fprintf(&output, "LITELLM_SALT_KEY=%s\n", quoteEnv(cfg.SaltKey))
	fmt.Fprintf(&output, "DATABASE_URL=%s\n", quoteEnv(databaseURL(cfg)))
	fmt.Fprintf(&output, "POSTGRES_DB=%s\n", quoteEnv(cfg.Database.Name))
	fmt.Fprintf(&output, "POSTGRES_USER=%s\n", quoteEnv(cfg.Database.User))
	fmt.Fprintf(&output, "POSTGRES_PASSWORD=%s\n", quoteEnv(cfg.Database.Password))
	fmt.Fprintf(&output, "STORE_MODEL_IN_DB=%t\n", cfg.StoreModelInDB)
	fmt.Fprintf(&output, "NUM_WORKERS=%d\n", cfg.NumWorkers)
	if _, err := io.WriteString(writer, output.String()); err != nil {
		return fmt.Errorf("输出运行环境失败: %w", err)
	}
	return nil
}

func WriteLiteLLM(writer io.Writer, cfg Config) error {
	runtime := liteLLMConfig{
		ModelList: make([]liteLLMModel, 0, len(cfg.Models)),
		GeneralSettings: liteLLMGeneralSettings{
			StoreModelInDB: cfg.StoreModelInDB, DatabaseConnectionPoolLimit: cfg.DatabasePoolLimit,
			ProxyBatchWriteAt: cfg.ProxyBatchWriteSeconds,
		},
		LiteLLMSettings: liteLLMSettings{
			RequestTimeout: cfg.RequestTimeout, JSONLogs: true, SetVerbose: false,
		},
	}
	for _, model := range cfg.Models {
		runtime.ModelList = append(runtime.ModelList, liteLLMModel{
			ModelName: model.ModelName,
			LiteLLMParams: liteLLMParams{
				Model: model.Model, APIBase: model.APIBase, APIKey: model.APIKey,
			},
		})
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(runtime); err != nil {
		return fmt.Errorf("生成 LiteLLM 配置失败: %w", err)
	}
	return nil
}

func WriteImages(writer io.Writer, cfg Config) error {
	if _, err := fmt.Fprintf(writer, "%s\n%s\n", cfg.LiteLLMImage, cfg.PostgresImage); err != nil {
		return fmt.Errorf("输出镜像清单失败: %w", err)
	}
	return nil
}

func databaseURL(cfg Config) string {
	value := &url.URL{
		Scheme: "postgresql",
		User:   url.UserPassword(cfg.Database.User, cfg.Database.Password),
		Host:   "db:5432",
		Path:   "/" + cfg.Database.Name,
	}
	return value.String()
}

func quoteEnv(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `\'`) + `'`
}

func isPlaceholder(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || strings.Contains(value, "change-me") || strings.Contains(value, "replace-me")
}
