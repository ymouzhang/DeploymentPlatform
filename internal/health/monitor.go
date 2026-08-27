package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"DP/internal/domain"
	"DP/internal/store"
)

type Monitor struct {
	store    *store.Store
	interval time.Duration
	client   *http.Client

	mu       sync.RWMutex
	results  map[string]domain.HealthResult
	failures map[string]int
	alerted  map[string]bool
}

func NewMonitor(store *store.Store, interval time.Duration) *Monitor {
	transport := &http.Transport{
		Proxy:              nil,
		MaxIdleConns:       20,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
	}
	return &Monitor{
		store: store, interval: interval,
		client: &http.Client{
			Timeout:   3 * time.Second,
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		results:  make(map[string]domain.HealthResult),
		failures: make(map[string]int), alerted: make(map[string]bool),
	}
}

func (m *Monitor) Run(ctx context.Context) {
	m.checkAll(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkAll(ctx)
		}
	}
}

func (m *Monitor) Snapshot(environmentID string) domain.HealthResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if result, exists := m.results[environmentID]; exists {
		return result
	}
	return domain.HealthResult{State: "not_configured"}
}

func (m *Monitor) CheckNow(ctx context.Context, environmentID string) (domain.HealthResult, error) {
	env, err := m.store.GetEnvironment(ctx, environmentID)
	if err != nil {
		return domain.HealthResult{}, err
	}
	result := m.check(ctx, env)
	m.recordResult(env, result)
	return result, nil
}

func (m *Monitor) checkAll(ctx context.Context) {
	environments, err := m.store.ListEnvironments(ctx)
	if err != nil {
		return
	}
	known := make(map[string]struct{}, len(environments))
	const workers = 8
	jobs := make(chan domain.Environment)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for env := range jobs {
				result := m.check(ctx, env)
				m.recordResult(env, result)
			}
		}()
	}
	for _, env := range environments {
		known[env.ID] = struct{}{}
		jobs <- env
	}
	close(jobs)
	wg.Wait()

	m.mu.Lock()
	for environmentID := range m.results {
		if _, exists := known[environmentID]; !exists {
			delete(m.results, environmentID)
			delete(m.failures, environmentID)
			delete(m.alerted, environmentID)
		}
	}
	m.mu.Unlock()
}

func (m *Monitor) recordResult(env domain.Environment, result domain.HealthResult) {
	shouldAlert := false
	m.mu.Lock()
	m.results[env.ID] = result
	if env.Installed && result.State != "running" && result.State != "not_configured" {
		m.failures[env.ID]++
		if m.failures[env.ID] >= 3 && !m.alerted[env.ID] {
			m.alerted[env.ID], shouldAlert = true, true
		}
	} else {
		m.failures[env.ID] = 0
		m.alerted[env.ID] = false
	}
	m.mu.Unlock()
	if shouldAlert {
		owner, _ := m.store.GetUser(context.Background(), env.OwnerID)
		_, _ = m.store.CreateNotification(context.Background(), domain.Notification{
			DedupeKey: "service-health:" + env.ID,
			RiskLevel: "high", Category: "operation", Title: "服务连续不可达",
			Message:    env.Name + " 连续三次健康检查未恢复：" + result.Reason,
			TargetType: "service", TargetID: env.ID, TargetLabel: env.Name,
			OwnerID: env.OwnerID, OwnerUsername: owner.Username, Link: "/services?owner_id=" + env.OwnerID,
		})
	}
}

func (m *Monitor) check(ctx context.Context, env domain.Environment) domain.HealthResult {
	now := time.Now().UTC()
	result := domain.HealthResult{State: "not_configured", CheckedAt: &now}
	if !env.Installed || env.HealthPort == nil {
		return result
	}
	host := net.JoinHostPort(env.IP, fmt.Sprintf("%d", *env.HealthPort))
	endpoint := url.URL{Scheme: "http", Host: host, Path: "/health"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		result.State, result.Reason = "invalid_response", "健康检查地址无效"
		return result
	}
	response, err := m.client.Do(request)
	if err != nil {
		result.State, result.Reason = "unreachable", "健康接口无法访问"
		return result
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		result.State, result.Reason = "invalid_response", "健康接口响应读取失败"
		return result
	}
	var body map[string]any
	if err := json.Unmarshal(content, &body); err != nil {
		result.State, result.Reason = "invalid_response", "健康接口未返回有效 JSON"
		return result
	}
	if status, ok := body["status"].(string); ok && (status == "health" || status == "healthy") {
		result.State = "running"
		return result
	}
	result.State, result.Reason = "stopped", `健康接口 status 不是 "health" 或 "healthy"`
	return result
}
