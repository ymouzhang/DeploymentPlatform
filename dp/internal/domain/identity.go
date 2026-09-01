package domain

import "github.com/google/uuid"

const InitialAdminID = "00000000-0000-4000-8000-000000000001"

func NewID() string {
	return uuid.NewString()
}
