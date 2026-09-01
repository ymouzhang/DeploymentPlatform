package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"DP/internal/domain"
)

type Repository interface {
	CreateNotification(context.Context, domain.Notification) (domain.Notification, error)
	GetEnvironment(context.Context, string) (domain.Environment, error)
	GetUser(context.Context, string) (domain.User, error)
	ListEnvironments(context.Context) ([]domain.Environment, error)
	Ping(context.Context) error
	ResolveNotificationByDedupeKey(context.Context, string, string, time.Time) error
}

type Monitor struct {
	store    Repository
	dataDir  string
	interval time.Duration
	client   *http.Client

	mu       sync.RWMutex
	results  map[string]domain.HealthResult
	failures map[string]int
	alerted  map[string]bool
	healthy  map[string]bool
}

const maxResponseBytes = 4 << 10

type ProbeResult struct {
	Status string `json:"status"`
}

func NewMonitor(store Repository, dataDir string, interval time.Duration) *Monitor {
	transport := &http.Transport{
		Proxy:              nil,
		MaxIdleConns:       20,
		IdleConnTimeout:    30 * time.Second,
		DisableCompression: true,
	}
	return &Monitor{
		store: store, dataDir: dataDir, interval: interval,
		client: &http.Client{
			Timeout:   3 * time.Second,
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		results:  make(map[string]domain.HealthResult),
		failures: make(map[string]int), alerted: make(map[string]bool), healthy: make(map[string]bool),
	}
}

func (m *Monitor) Health(ctx context.Context) ProbeResult {
	if m.store == nil || m.store.Ping(ctx) != nil {
		return ProbeResult{Status: "error"}
	}
	file, err := os.CreateTemp(m.dataDir, ".dp-health-*")
	if err != nil {
		return ProbeResult{Status: "error"}
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return ProbeResult{Status: "error"}
	}
	if err := os.Remove(name); err != nil {
		return ProbeResult{Status: "error"}
	}
	return ProbeResult{Status: "ok"}
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
		staleAfter := 3 * m.interval
		if minimum := 3 * m.client.Timeout; staleAfter < minimum {
			staleAfter = minimum
		}
		if result.CheckedAt != nil && time.Since(*result.CheckedAt) > staleAfter {
			result.Status = "unknown"
		}
		return result
	}
	return domain.HealthResult{Status: "unknown"}
}

func (m *Monitor) CheckNow(ctx context.Context, environmentID string) (domain.HealthResult, error) {
	env, err := m.store.GetEnvironment(ctx, environmentID)
	if err != nil {
		return domain.HealthResult{}, err
	}
	result := m.check(ctx, env)
	m.recordResult(ctx, env, result)
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
				m.recordResult(ctx, env, result)
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
			delete(m.healthy, environmentID)
		}
	}
	m.mu.Unlock()
}

func (m *Monitor) recordResult(ctx context.Context, env domain.Environment, result domain.HealthResult) {
	healthy := result.Status == "ok" || !env.Installed
	shouldAlert, shouldResolve := false, false
	m.mu.Lock()
	m.results[env.ID] = result
	if env.Installed && !healthy {
		m.failures[env.ID]++
		if m.failures[env.ID] >= 3 && !m.alerted[env.ID] {
			m.alerted[env.ID], shouldAlert = true, true
		}
	} else {
		shouldResolve = !m.healthy[env.ID]
		m.failures[env.ID] = 0
		m.alerted[env.ID] = false
	}
	m.healthy[env.ID] = healthy
	m.mu.Unlock()
	if !shouldAlert && !shouldResolve {
		return
	}
	notificationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if shouldResolve {
		_ = m.store.ResolveNotificationByDedupeKey(notificationCtx, "service-health:"+env.ID, "system", time.Now().UTC())
	}
	if shouldAlert {
		owner, _ := m.store.GetUser(notificationCtx, env.OwnerID)
		_, _ = m.store.CreateNotification(notificationCtx, domain.Notification{
			DedupeKey: "service-health:" + env.ID,
			RiskLevel: "high", Category: "operation", Title: "服务连续不可达",
			Message:    env.Name + " 连续三次健康检查未恢复",
			TargetType: "service", TargetID: env.ID, TargetLabel: env.Name,
			OwnerID: env.OwnerID, OwnerUsername: owner.Username, Link: "/services?owner_id=" + env.OwnerID,
		})
	}
}

func (m *Monitor) check(ctx context.Context, env domain.Environment) (result domain.HealthResult) {
	now := time.Now().UTC()
	result = domain.HealthResult{Status: "unknown", CheckedAt: &now}
	if !env.Installed || env.HealthPort == nil {
		return result
	}
	host := net.JoinHostPort(env.IP, fmt.Sprintf("%d", *env.HealthPort))
	endpoint := url.URL{Scheme: "http", Host: host, Path: "/healthz"}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		result.Status = "error"
		return result
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "DP-HealthChecker/1.0")
	response, err := m.client.Do(request)
	if err != nil {
		result.Status = "error"
		return result
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		result.Status = "error"
		return result
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		result.Status = "error"
		return result
	}
	if len(content) > maxResponseBytes {
		result.Status = "error"
		return result
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(content, &body); err != nil {
		result.Status = "error"
		return result
	}
	if body.Status == "ok" {
		result.Status = "ok"
	} else {
		result.Status = "error"
	}
	return result
}
