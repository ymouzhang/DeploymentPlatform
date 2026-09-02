package application

import (
	"context"
	"errors"
	"testing"

	"DP/internal/domain"
)

type instanceStoreStub struct {
	host     domain.Host
	instance domain.ServiceInstance
	pkg      domain.Package
}

func (s *instanceStoreStub) CreateServiceInstanceWithTags(_ context.Context, item domain.ServiceInstance, _ []string) (domain.ServiceInstance, error) {
	item.ID = "instance"
	item.Host = s.host
	s.instance = item
	return item, nil
}
func (s *instanceStoreStub) UpdateServiceInstanceWithTags(_ context.Context, item domain.ServiceInstance, _ []string) (domain.ServiceInstance, error) {
	item.Host = s.host
	s.instance = item
	return item, nil
}
func (*instanceStoreStub) DeleteServiceInstance(context.Context, string) ([]string, error) {
	return nil, nil
}
func (s *instanceStoreStub) GetServiceInstance(context.Context, string) (domain.ServiceInstance, error) {
	if s.instance.ID == "" {
		return domain.ServiceInstance{}, domain.ErrNotFound
	}
	return s.instance, nil
}
func (s *instanceStoreStub) GetHost(context.Context, string) (domain.Host, error) { return s.host, nil }
func (s *instanceStoreStub) GetPackageByOwner(context.Context, string, string) (domain.Package, error) {
	if s.pkg.ServiceType == "" {
		return domain.Package{}, domain.ErrNotFound
	}
	return s.pkg, nil
}
func (s *instanceStoreStub) ListServiceInstances(context.Context) ([]domain.ServiceInstance, error) {
	return []domain.ServiceInstance{s.instance}, nil
}
func (s *instanceStoreStub) ListServiceInstancesByOwner(context.Context, string) ([]domain.ServiceInstance, error) {
	return []domain.ServiceInstance{s.instance}, nil
}
func (*instanceStoreStub) ValidateTagIDs(context.Context, string, []string) error { return nil }

func TestServiceInstanceCreateLinksExistingHostAndPackage(t *testing.T) {
	store := &instanceStoreStub{host: domain.Host{ID: "host", OwnerID: "owner", IP: "192.0.2.1"}, pkg: domain.Package{ServiceType: "vllm"}}
	result, err := NewServiceInstanceService(store).Create(context.Background(), "owner", domain.ServiceInstanceInput{HostID: "host", Name: "qwen", InstallDir: "/opt/qwen", ServiceType: "vllm"})
	if err != nil {
		t.Fatal(err)
	}
	if result.HostID != "host" || result.Host.IP != "192.0.2.1" {
		t.Fatalf("unexpected instance: %+v", result)
	}
}

func TestServiceInstanceCreateRequiresPackage(t *testing.T) {
	store := &instanceStoreStub{host: domain.Host{ID: "host", OwnerID: "owner"}}
	_, err := NewServiceInstanceService(store).Create(context.Background(), "owner", domain.ServiceInstanceInput{HostID: "host", Name: "qwen", InstallDir: "/opt/qwen", ServiceType: "vllm"})
	var field *domain.FieldValidationError
	if !errors.As(err, &field) || field.Field != "service_type" {
		t.Fatalf("expected service_type error, got %v", err)
	}
}

func TestInstalledInstanceCannotMoveHost(t *testing.T) {
	store := &instanceStoreStub{host: domain.Host{ID: "host-b", OwnerID: "owner"}, pkg: domain.Package{ServiceType: "vllm"}, instance: domain.ServiceInstance{ID: "instance", OwnerID: "owner", HostID: "host-a", Name: "qwen", InstallDir: "/opt/qwen", ServiceType: "vllm", Installed: true}}
	_, err := NewServiceInstanceService(store).Update(context.Background(), "instance", domain.ServiceInstanceInput{HostID: "host-b", Name: "qwen", InstallDir: "/opt/qwen", ServiceType: "vllm"})
	if err == nil {
		t.Fatal("expected installed immutability error")
	}
}
