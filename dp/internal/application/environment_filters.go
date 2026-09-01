package application

import "DP/internal/domain"

func FilterEnvironmentsByTagIDs(
	environments []domain.Environment,
	tagIDs []string,
) []domain.Environment {
	if len(tagIDs) == 0 {
		return environments
	}
	required := make(map[string]struct{}, len(tagIDs))
	for _, id := range tagIDs {
		required[id] = struct{}{}
	}
	result := make([]domain.Environment, 0, len(environments))
	for _, environment := range environments {
		matched := 0
		for _, tag := range environment.Tags {
			if _, ok := required[tag.ID]; ok {
				matched++
			}
		}
		if matched == len(required) {
			result = append(result, environment)
		}
	}
	return result
}
