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

func TestModelUploadCreateInputValidation(t *testing.T) {
	valid := ModelUploadCreateInput{Name: "Qwen3", EnvironmentID: "env", TargetDir: "/opt/models/Qwen3", OriginalFilename: "qwen.tar.gz", TotalBytes: 40 << 30}
	if err := valid.Validate(1 << 40); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	for name, mutate := range map[string]func(*ModelUploadCreateInput){
		"relative target": func(v *ModelUploadCreateInput) { v.TargetDir = "models/Qwen3" },
		"critical target": func(v *ModelUploadCreateInput) { v.TargetDir = "/etc" },
		"wrong extension": func(v *ModelUploadCreateInput) { v.OriginalFilename = "model.zip" },
		"too large":       func(v *ModelUploadCreateInput) { v.TotalBytes = 2 << 40 },
	} {
		t.Run(name, func(t *testing.T) {
			value := valid
			mutate(&value)
			if err := value.Validate(1 << 40); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
