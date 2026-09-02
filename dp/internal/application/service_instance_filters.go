package application

import "DP/internal/domain"

func FilterServiceInstancesByTagIDs(
	instances []domain.ServiceInstance,
	tagIDs []string,
) []domain.ServiceInstance {
	if len(tagIDs) == 0 {
		return instances
	}
	required := make(map[string]struct{}, len(tagIDs))
	for _, id := range tagIDs {
		required[id] = struct{}{}
	}
	result := make([]domain.ServiceInstance, 0, len(instances))
	for _, instance := range instances {
		matched := 0
		for _, tag := range instance.Tags {
			if _, ok := required[tag.ID]; ok {
				matched++
			}
		}
		if matched == len(required) {
			result = append(result, instance)
		}
	}
	return result
}
