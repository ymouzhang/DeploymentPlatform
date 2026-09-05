package remote

import (
	"testing"
	"time"
)

func TestConfigureOperationTimeouts(t *testing.T) {
	executor := NewExecutor(time.Minute)
	executor.ConfigureOperationTimeouts(90*time.Minute, 3*time.Hour)
	if executor.extractTimeout != 90*time.Minute || executor.scriptTimeout != 3*time.Hour {
		t.Fatalf("timeouts not applied: extract=%s script=%s", executor.extractTimeout, executor.scriptTimeout)
	}
}

func TestShellQuote(t *testing.T) {
	got := shellQuote("/opt/a'b")
	want := `'/opt/a'"'"'b'`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildExtractCommandStripsWrapperDirectory(t *testing.T) {
	got := buildExtractCommand("/tmp/package.tar.gz", "/opt/demo service", true)
	want := "tar -xzf '/tmp/package.tar.gz' -C '/opt/demo service' --strip-components=1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestComposeLogCommandInputsAreQuoted(t *testing.T) {
	got := buildComposeLogCommand("/opt/a'b", 300)
	want := `cd '/opt/a'"'"'b' && exec docker compose --ansi always logs --follow --tail 300 --timestamps`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
