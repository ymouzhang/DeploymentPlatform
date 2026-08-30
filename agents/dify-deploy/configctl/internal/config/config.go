package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Port                    int      `json:"port"`
	APIPort                 int      `json:"api_port"`
	PublicURL               string   `json:"public_url"`
	Images                  Images   `json:"images"`
	Database                Database `json:"database"`
	RedisPassword           string   `json:"redis_password"`
	SecretKey               string   `json:"secret_key"`
	SandboxAPIKey           string   `json:"sandbox_api_key"`
	PluginDaemonKey         string   `json:"plugin_daemon_key"`
	PluginInnerAPIKey       string   `json:"plugin_inner_api_key"`
	AgentAPIToken           string   `json:"agent_api_token"`
	AgentServerSecretKey    string   `json:"agent_server_secret_key"`
	AgentShellAuthToken     string   `json:"agent_shell_auth_token"`
	WeaviateAPIKey          string   `json:"weaviate_api_key"`
	WeaviateAdminUser       string   `json:"weaviate_admin_user"`
	ServerWorkers           int      `json:"server_workers"`
	CeleryWorkers           int      `json:"celery_workers"`
	PrivateNetworkAllowlist []string `json:"private_network_allowlist"`
}

type Images struct {
	API          string `json:"api"`
	Web          string `json:"web"`
	BundledAPI   string `json:"bundled_api"`
	Nginx        string `json:"nginx"`
	Busybox      string `json:"busybox"`
	Sandbox      string `json:"sandbox"`
	Weaviate     string `json:"weaviate"`
	LocalSandbox string `json:"local_sandbox"`
	Redis        string `json:"redis"`
	PluginDaemon string `json:"plugin_daemon"`
	Postgres     string `json:"postgres"`
	AgentBackend string `json:"agent_backend"`
	Squid        string `json:"squid"`
}

type Database struct {
	Name     string `json:"name"`
	User     string `json:"user"`
	Password string `json:"password"`
}

var (
	imagePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]*$`)
	idPattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
	secretPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]+$`)
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
	publicURL, err := url.Parse(cfg.PublicURL)
	if err != nil || publicURL.Scheme != "http" || publicURL.Hostname() == "" || publicURL.Path != "" || publicURL.RawQuery != "" || publicURL.Fragment != "" {
		return errors.New("public_url 必须是无路径、查询参数和片段的 http 地址")
	}
	publicPort := 80
	if publicURL.Port() != "" {
		publicPort, err = strconv.Atoi(publicURL.Port())
		if err != nil {
			return errors.New("public_url 端口无效")
		}
	}
	if publicPort != cfg.APIPort {
		return errors.New("public_url 的端口必须与 api_port 相同")
	}
	for name, image := range imageMap(cfg.Images) {
		if !imagePattern.MatchString(image) {
			return fmt.Errorf("images.%s 不是有效的容器镜像引用", name)
		}
	}
	if !idPattern.MatchString(cfg.Database.Name) || !idPattern.MatchString(cfg.Database.User) {
		return errors.New("database.name 和 database.user 只能包含字母、数字、下划线和连字符")
	}
	secrets := map[string]string{
		"database.password":       cfg.Database.Password,
		"redis_password":          cfg.RedisPassword,
		"secret_key":              cfg.SecretKey,
		"sandbox_api_key":         cfg.SandboxAPIKey,
		"plugin_daemon_key":       cfg.PluginDaemonKey,
		"plugin_inner_api_key":    cfg.PluginInnerAPIKey,
		"agent_api_token":         cfg.AgentAPIToken,
		"agent_server_secret_key": cfg.AgentServerSecretKey,
		"agent_shell_auth_token":  cfg.AgentShellAuthToken,
		"weaviate_api_key":        cfg.WeaviateAPIKey,
	}
	for name, value := range secrets {
		if len(value) < 24 || !secretPattern.MatchString(value) {
			return fmt.Errorf("%s 长度至少为 24，且只能包含 URL 安全的密钥字符", name)
		}
		if requireSecrets && isPlaceholder(value) {
			return fmt.Errorf("%s 仍是占位值，请先修改", name)
		}
	}
	if !idPattern.MatchString(cfg.WeaviateAdminUser) {
		return errors.New("weaviate_admin_user 只能包含字母、数字、下划线和连字符")
	}
	if cfg.ServerWorkers < 1 || cfg.ServerWorkers > 32 || cfg.CeleryWorkers < 1 || cfg.CeleryWorkers > 64 {
		return errors.New("server_workers 必须为 1 到 32，celery_workers 必须为 1 到 64")
	}
	if len(cfg.PrivateNetworkAllowlist) > 32 {
		return errors.New("private_network_allowlist 最多包含 32 个网段")
	}
	for index, network := range cfg.PrivateNetworkAllowlist {
		ip, parsed, err := net.ParseCIDR(network)
		if err != nil || ip == nil || parsed == nil {
			return fmt.Errorf("private_network_allowlist[%d] 不是有效 CIDR", index)
		}
		if ones, bits := parsed.Mask.Size(); ones == 0 && (bits == 32 || bits == 128) {
			return fmt.Errorf("private_network_allowlist[%d] 不能允许整个互联网", index)
		}
	}
	return nil
}

func WriteEnv(writer io.Writer, cfg Config) error {
	publicURL := strings.TrimSuffix(cfg.PublicURL, "/")
	socketURL := "ws" + strings.TrimPrefix(publicURL, "http")
	redisURL := &url.URL{Scheme: "redis", User: url.UserPassword("", cfg.RedisPassword), Host: "redis:6379", Path: "/2"}
	celeryURL := &url.URL{Scheme: "redis", User: url.UserPassword("", cfg.RedisPassword), Host: "redis:6379", Path: "/1"}
	values := [][2]string{
		{"SERVICE_PORT", strconv.Itoa(cfg.Port)}, {"API_PORT", strconv.Itoa(cfg.APIPort)},
		{"DIFY_API_IMAGE", cfg.Images.API}, {"DIFY_WEB_IMAGE", cfg.Images.Web}, {"DIFY_BUNDLED_API_IMAGE", cfg.Images.BundledAPI},
		{"NGINX_IMAGE", cfg.Images.Nginx}, {"BUSYBOX_IMAGE", cfg.Images.Busybox}, {"SANDBOX_IMAGE", cfg.Images.Sandbox},
		{"WEAVIATE_IMAGE", cfg.Images.Weaviate}, {"LOCAL_SANDBOX_IMAGE", cfg.Images.LocalSandbox}, {"REDIS_IMAGE", cfg.Images.Redis},
		{"PLUGIN_DAEMON_IMAGE", cfg.Images.PluginDaemon}, {"POSTGRES_IMAGE", cfg.Images.Postgres},
		{"AGENT_BACKEND_IMAGE", cfg.Images.AgentBackend}, {"SQUID_IMAGE", cfg.Images.Squid},
		{"COMPOSE_PROFILES", "postgresql,weaviate,collaboration"}, {"DB_TYPE", "postgresql"}, {"VECTOR_STORE", "weaviate"},
		{"STORAGE_TYPE", "opendal"}, {"OPENDAL_SCHEME", "fs"}, {"OPENDAL_FS_ROOT", "storage"},
		{"CONSOLE_API_URL", publicURL}, {"CONSOLE_WEB_URL", publicURL}, {"SERVICE_API_URL", publicURL},
		{"APP_API_URL", publicURL}, {"APP_WEB_URL", publicURL}, {"FILES_URL", publicURL}, {"TRIGGER_URL", publicURL},
		{"ENDPOINT_URL_TEMPLATE", publicURL + "/e/{hook_id}"}, {"NEXT_PUBLIC_SOCKET_URL", socketURL},
		{"SECRET_KEY", cfg.SecretKey}, {"DEPLOY_ENV", "PRODUCTION"}, {"MIGRATION_ENABLED", "true"},
		{"CHECK_UPDATE_URL", ""}, {"MARKETPLACE_ENABLED", "false"}, {"MARKETPLACE_API_URL", ""}, {"MARKETPLACE_URL", ""},
		{"DB_HOST", "db_postgres"}, {"DB_PORT", "5432"}, {"DB_DATABASE", cfg.Database.Name},
		{"DB_USERNAME", cfg.Database.User}, {"DB_PASSWORD", cfg.Database.Password},
		{"REDIS_PASSWORD", cfg.RedisPassword}, {"CELERY_BROKER_URL", celeryURL.String()},
		{"SANDBOX_API_KEY", cfg.SandboxAPIKey}, {"CODE_EXECUTION_API_KEY", cfg.SandboxAPIKey},
		{"PLUGIN_DAEMON_KEY", cfg.PluginDaemonKey}, {"PLUGIN_DIFY_INNER_API_KEY", cfg.PluginInnerAPIKey},
		{"DIFY_AGENT_INNER_API_KEY", cfg.PluginInnerAPIKey}, {"DIFY_AGENT_API_TOKEN", cfg.AgentAPIToken},
		{"DIFY_AGENT_SERVER_SECRET_KEY", cfg.AgentServerSecretKey}, {"DIFY_AGENT_SHELLCTL_AUTH_TOKEN", cfg.AgentShellAuthToken},
		{"DIFY_AGENT_REDIS_URL", redisURL.String()}, {"WEAVIATE_API_KEY", cfg.WeaviateAPIKey},
		{"WEAVIATE_AUTHENTICATION_APIKEY_ALLOWED_KEYS", cfg.WeaviateAPIKey},
		{"WEAVIATE_AUTHENTICATION_APIKEY_USERS", cfg.WeaviateAdminUser}, {"WEAVIATE_AUTHORIZATION_ADMINLIST_USERS", cfg.WeaviateAdminUser},
		{"WEAVIATE_AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED", "false"}, {"WEAVIATE_DISABLE_TELEMETRY", "true"},
		{"SERVER_WORKER_AMOUNT", strconv.Itoa(cfg.ServerWorkers)}, {"CELERY_WORKER_AMOUNT", strconv.Itoa(cfg.CeleryWorkers)},
		{"ENABLE_COLLABORATION_MODE", "true"}, {"COMPOSE_WORKER_HEALTHCHECK_DISABLED", "true"},
		{"FORCE_VERIFYING_SIGNATURE", "false"}, {"NEW_USER_DEFAULT_PLUGIN_IDS", "langgenius/openai_api_compatible,langgenius/agent"},
		{"SSRF_PROXY_ALLOW_PRIVATE_IPS", strings.Join(cfg.PrivateNetworkAllowlist, " ")},
		{"NGINX_HTTPS_ENABLED", "false"}, {"NGINX_PORT", "80"}, {"NGINX_ENABLE_CERTBOT_CHALLENGE", "false"},
	}
	var output strings.Builder
	for _, item := range values {
		fmt.Fprintf(&output, "%s=%s\n", item[0], quoteEnv(item[1]))
	}
	if _, err := io.WriteString(writer, output.String()); err != nil {
		return fmt.Errorf("输出运行环境失败: %w", err)
	}
	return nil
}

func WriteImages(writer io.Writer, cfg Config) error {
	seen := make(map[string]struct{})
	for _, image := range imageList(cfg.Images) {
		if _, exists := seen[image]; exists {
			continue
		}
		seen[image] = struct{}{}
		if _, err := fmt.Fprintln(writer, image); err != nil {
			return fmt.Errorf("输出镜像清单失败: %w", err)
		}
	}
	return nil
}

func imageMap(images Images) map[string]string {
	return map[string]string{"api": images.API, "web": images.Web, "bundled_api": images.BundledAPI, "nginx": images.Nginx,
		"busybox": images.Busybox, "sandbox": images.Sandbox, "weaviate": images.Weaviate, "local_sandbox": images.LocalSandbox,
		"redis": images.Redis, "plugin_daemon": images.PluginDaemon, "postgres": images.Postgres,
		"agent_backend": images.AgentBackend, "squid": images.Squid}
}

func imageList(images Images) []string {
	return []string{images.API, images.Web, images.BundledAPI, images.Nginx, images.Busybox, images.Sandbox,
		images.Weaviate, images.LocalSandbox, images.Redis, images.PluginDaemon, images.Postgres, images.AgentBackend, images.Squid}
}

func quoteEnv(value string) string { return `'` + strings.ReplaceAll(value, `'`, `\'`) + `'` }

func isPlaceholder(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "" || strings.Contains(value, "change-me") || strings.Contains(value, "replace-me")
}
