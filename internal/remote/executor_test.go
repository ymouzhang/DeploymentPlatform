package remote

import "testing"

func TestShellQuote(t *testing.T) {
	got := shellQuote("/opt/a'b")
	want := `'/opt/a'"'"'b'`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestBuildExtractCommandStripsWrapperDirectory(t *testing.T) {
	got := buildExtractCommand("/tmp/package.tar.gz", "/opt/image forward", true)
	want := "tar -xzf '/tmp/package.tar.gz' -C '/opt/image forward' --strip-components=1"
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
