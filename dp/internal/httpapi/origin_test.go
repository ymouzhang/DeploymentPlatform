package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestSameOriginRequest(t *testing.T) {
	tests := []struct {
		name           string
		host           string
		origin         string
		forwardedHost  string
		forwardedProto string
		trustedProxy   bool
		want           bool
	}{
		{name: "no browser origin", host: "127.0.0.1:8080", want: true},
		{name: "direct same host", host: "localhost:5173", origin: "http://localhost:5173", want: true},
		{
			name: "reverse proxy public host", host: "127.0.0.1:8080",
			origin: "https://dp.internal", forwardedHost: "dp.internal", forwardedProto: "https", trustedProxy: true, want: true,
		},
		{name: "untrusted forwarded host", host: "127.0.0.1:8080", origin: "https://dp.internal", forwardedHost: "dp.internal", forwardedProto: "https", want: false},
		{name: "scheme mismatch", host: "dp.internal", origin: "https://dp.internal", want: false},
		{name: "cross site", host: "dp.internal", origin: "https://evil.example", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://"+test.host+"/api/v1/hosts", nil)
			request.RemoteAddr = "10.0.0.2:1234"
			request.Host = test.host
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.forwardedHost != "" {
				request.Header.Set("X-Forwarded-Host", test.forwardedHost)
			}
			if test.forwardedProto != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwardedProto)
			}
			api := &API{}
			if test.trustedProxy {
				api.trustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
			}
			if got := api.sameOriginRequest(request); got != test.want {
				t.Fatalf("sameOriginRequest()=%v, want %v", got, test.want)
			}
		})
	}
}
