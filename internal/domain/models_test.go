package domain

import "testing"

func TestValidateServiceType(t *testing.T) {
	for _, value := range []string{"dp-demo", "video2", "a"} {
		if err := ValidateServiceType(value); err != nil {
			t.Fatalf("expected %q to be valid: %v", value, err)
		}
	}
	for _, value := range []string{"", "DP-Demo", "2video", "video_forward", "../video"} {
		if err := ValidateServiceType(value); err == nil {
			t.Fatalf("expected %q to be invalid", value)
		}
	}
}
