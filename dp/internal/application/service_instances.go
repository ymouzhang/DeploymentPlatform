package application

import (
	"context"
	"errors"

	"DP/internal/domain"
)

type ServiceInstanceRepository interface {
	CreateServiceInstanceWithTags(context.Context, domain.ServiceInstance, []string) (domain.ServiceInstance, error)
	UpdateServiceInstanceWithTags(context.Context, domain.ServiceInstance, []string) (domain.ServiceInstance, error)
	DeleteServiceInstance(context.Context, string) ([]string, error)
	GetServiceInstance(context.Context, string) (domain.ServiceInstance, error)
	GetHost(context.Context, string) (domain.Host, error)
	GetPackageByOwner(context.Context, string, string) (domain.Package, error)
	ListServiceInstances(context.Context) ([]domain.ServiceInstance, error)
	ListServiceInstancesByOwner(context.Context, string) ([]domain.ServiceInstance, error)
	ValidateTagIDs(context.Context, string, []string) error
}

type ServiceInstanceService struct{ store ServiceInstanceRepository }

func NewServiceInstanceService(store ServiceInstanceRepository) *ServiceInstanceService {
	return &ServiceInstanceService{store: store}
}

func (s *ServiceInstanceService) List(ctx context.Context, ownerID string) ([]domain.ServiceInstance, error) {
	return s.ListFiltered(ctx, ownerID, nil)
}

func (s *ServiceInstanceService) ListFiltered(ctx context.Context, ownerID string, tagIDs []string) ([]domain.ServiceInstance, error) {
	var items []domain.ServiceInstance
	var err error
	if ownerID == "" {
		items, err = s.store.ListServiceInstances(ctx)
	} else {
		items, err = s.store.ListServiceInstancesByOwner(ctx, ownerID)
	}
	if err != nil {
		return nil, err
	}
	return FilterServiceInstancesByTagIDs(items, tagIDs), nil
}

func (s *ServiceInstanceService) Create(ctx context.Context, ownerID string, input domain.ServiceInstanceInput) (domain.ServiceInstance, error) {
	input.Normalize()
	if err := input.Validate(); err != nil {
		return domain.ServiceInstance{}, err
	}
	host, err := s.store.GetHost(ctx, input.HostID)
	if err != nil {
		return domain.ServiceInstance{}, err
	}
	if host.OwnerID != ownerID {
		return domain.ServiceInstance{}, domain.ErrForbidden
	}
	if err := s.requireServicePackage(ctx, ownerID, input.ServiceType); err != nil {
		return domain.ServiceInstance{}, err
	}
	if err := s.store.ValidateTagIDs(ctx, ownerID, input.TagIDs); err != nil {
		return domain.ServiceInstance{}, err
	}
	result, err := s.store.CreateServiceInstanceWithTags(ctx, domain.ServiceInstance{OwnerID: ownerID, HostID: host.ID,
		Name: input.Name, InstallDir: input.InstallDir, ServiceType: input.ServiceType, Note: input.Note}, input.TagIDs)
	if errors.Is(err, domain.ErrConflict) {
		return domain.ServiceInstance{}, &domain.AppError{Code: "SERVICE_INSTANCE_CONFLICT", Message: "该主机的安装目录已被其他服务实例使用"}
	}
	return result, err
}

func (s *ServiceInstanceService) Update(ctx context.Context, id string, input domain.ServiceInstanceInput) (domain.ServiceInstance, error) {
	input.Normalize()
	if err := input.Validate(); err != nil {
		return domain.ServiceInstance{}, err
	}
	instance, err := s.store.GetServiceInstance(ctx, id)
	if err != nil {
		return domain.ServiceInstance{}, err
	}
	host, err := s.store.GetHost(ctx, input.HostID)
	if err != nil {
		return domain.ServiceInstance{}, err
	}
	if host.OwnerID != instance.OwnerID {
		return domain.ServiceInstance{}, domain.ErrForbidden
	}
	if instance.Installed && (instance.HostID != input.HostID || instance.ServiceType != input.ServiceType || instance.InstallDir != input.InstallDir) {
		return domain.ServiceInstance{}, domain.FieldError("service_instance", "已安装实例不允许修改主机、服务类型或安装目录")
	}
	if err := s.requireServicePackage(ctx, instance.OwnerID, input.ServiceType); err != nil {
		return domain.ServiceInstance{}, err
	}
	if input.TagIDs != nil {
		if err := s.store.ValidateTagIDs(ctx, instance.OwnerID, input.TagIDs); err != nil {
			return domain.ServiceInstance{}, err
		}
	}
	instance.HostID, instance.Name, instance.InstallDir = input.HostID, input.Name, input.InstallDir
	instance.ServiceType, instance.Note = input.ServiceType, input.Note
	result, err := s.store.UpdateServiceInstanceWithTags(ctx, instance, input.TagIDs)
	if errors.Is(err, domain.ErrConflict) {
		return domain.ServiceInstance{}, &domain.AppError{Code: "SERVICE_INSTANCE_CONFLICT", Message: "该主机的安装目录已被其他服务实例使用"}
	}
	return result, err
}

func (s *ServiceInstanceService) Delete(ctx context.Context, id string) error {
	instance, err := s.store.GetServiceInstance(ctx, id)
	if err != nil {
		return err
	}
	if instance.Installed {
		return &domain.AppError{Code: "SERVICE_INSTALLED", Message: "请先重置已安装服务，再删除实例"}
	}
	_, err = s.store.DeleteServiceInstance(ctx, id)
	return err
}

func (s *ServiceInstanceService) requireServicePackage(ctx context.Context, ownerID, serviceType string) error {
	if _, err := s.store.GetPackageByOwner(ctx, ownerID, serviceType); errors.Is(err, domain.ErrNotFound) {
		return domain.FieldError("service_type", "该服务类型尚未上传安装包")
	} else if err != nil {
		return err
	}
	return nil
}
