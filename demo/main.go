package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

type config struct {
	Port        int    `json:"port"`
	ServiceName string `json:"service_name"`
}

func main() {
	configPath := flag.String("config", "config/config.json", "配置文件路径")
	healthcheckPath := flag.String("healthcheck", "", "检查指定配置对应的服务健康状态")
	flag.Parse()

	if *healthcheckPath != "" {
		if err := checkHealth(*healthcheckPath); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(*configPath); err != nil {
		log.Fatal(err)
	}
}

func run(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"service": cfg.ServiceName,
			"message": "DP demo service is running",
		})
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"status":  "health",
			"service": cfg.ServiceName,
		})
	})

	server := &http.Server{
		Addr:              net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.Port)),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	stopContext, stop := signal.NotifyContext(
		context.Background(), syscall.SIGINT, syscall.SIGTERM,
	)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("%s listening on %s", cfg.ServiceName, server.Addr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-stopContext.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdownContext)
	}
}

func loadConfig(path string) (config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("读取配置: %w", err)
	}
	var cfg config
	if err := json.Unmarshal(content, &cfg); err != nil {
		return config{}, fmt.Errorf("解析配置: %w", err)
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return config{}, errors.New("配置 port 必须在 1 到 65535 之间")
	}
	if cfg.ServiceName == "" {
		cfg.ServiceName = "dp-demo"
	}
	return cfg, nil
}

func checkHealth(configPath string) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	endpoint := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)) + "/health"
	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("请求健康接口: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("健康接口返回 HTTP %d", response.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return fmt.Errorf("解析健康响应: %w", err)
	}
	if body.Status != "health" {
		return fmt.Errorf("健康状态异常: %q", body.Status)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
