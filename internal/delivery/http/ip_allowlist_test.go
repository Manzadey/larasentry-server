package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPAllowlist(t *testing.T) {
	list := NewIPAllowlist([]string{"192.168.1.100", " 10.0.0.5 ", "not-an-ip"})
	handler := list.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    int
	}{
		{"configured IP", "192.168.1.100:5000", nil, http.StatusNoContent},
		{"configured IP with spaces", "10.0.0.5:5000", nil, http.StatusNoContent},
		{"unknown IP", "203.0.113.7:5000", nil, http.StatusForbidden},
		{"loopback v4", "127.0.0.1:5000", nil, http.StatusNoContent},
		{"loopback v6", "[::1]:5000", nil, http.StatusNoContent},
		{"spoofed header from outside", "203.0.113.7:5000", map[string]string{"X-Real-IP": "127.0.0.1"}, http.StatusForbidden},
		{"local proxy forwards allowed client", "127.0.0.1:5000", map[string]string{"X-Real-IP": "192.168.1.100"}, http.StatusNoContent},
		{"local proxy forwards unknown client", "127.0.0.1:5000", map[string]string{"X-Real-IP": "203.0.113.7"}, http.StatusForbidden},
		{"client-forged start of X-Forwarded-For", "127.0.0.1:5000", map[string]string{"X-Forwarded-For": "192.168.1.100, 203.0.113.7"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/events", nil)
			req.RemoteAddr = tc.remote
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("got %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
