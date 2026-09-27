package demo

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDMiddleware(t *testing.T) {
	seen := map[string]bool{}
	for _, supplied := range []string{"existing-request.42", "", "", strings.Repeat("x", 129), "bad id"} {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Request-Id", supplied)
		rec := httptest.NewRecorder()
		requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := requestID(r.Context())
			if id == "" || id != r.Header.Get("X-Request-Id") {
				t.Fatal("missing request context")
			}
		})).ServeHTTP(rec, req)
		id := rec.Header().Get("X-Request-Id")
		if !validRequestID.MatchString(id) {
			t.Fatalf("invalid generated ID: %q", id)
		}
		if supplied == "existing-request.42" && id != supplied {
			t.Fatal("existing ID changed")
		}
		if seen[id] {
			t.Fatal("request ID reused")
		}
		seen[id] = true
	}
}
func TestBodyRedaction(t *testing.T) {
	raw := []byte(`{"user_id":1001,"note":"keep this","large":9223372036854775807,"nested":[{"password":"private","ACCESS_TOKEN":"private","api-key":"private","cardNumber":"private","cvv":123,"session":{"id":"private"},"authorization":"private","cookie":"private","refresh_token":"private","credit_card":"private"}],"token":"private"}`)
	value, err := sanitizedBody(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := value.(map[string]any)["user_id"].(int64); !ok {
		t.Fatal("integer is not compatible with OTLP logs")
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private")) {
		t.Fatalf("secret leaked: %s", data)
	}
	for _, want := range []string{"keep this", "9223372036854775807", "1001", "[REDACTED]"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("missing %s", want)
		}
	}
	if _, err := sanitizedBody([]byte(`{"password":"private"`)); err == nil {
		t.Fatal("accepted malformed JSON")
	}
	if _, err := sanitizedBody([]byte(`{} {}`)); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}
func TestHeaderAllowlist(t *testing.T) {
	headers := http.Header{}
	for _, name := range []string{"Content-Type", "Accept", "User-Agent", "X-Request-Id", "X-Client-Version"} {
		headers.Set(name, "safe")
	}
	for _, name := range []string{"Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "X-Access-Token", "X-Custom"} {
		headers.Set(name, "private")
	}
	got := safeHeaders(headers)
	if len(got) != 5 {
		t.Fatal(got)
	}
	for _, value := range got {
		if value != "safe" {
			t.Fatal("header leaked")
		}
	}
}
func TestRequestLogPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"valid", `{"user_id":1001,"note":"visible","nested":{"token":"private"}}`, 200},
		{"malformed", `{"password":"private"`, 400},
		{"oversized", `{"password":"private","note":"` + strings.Repeat("x", maxBodyBytes) + `"}`, 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			defer slog.SetDefault(old)
			req := httptest.NewRequest("POST", "/checkout", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "private")
			rec := httptest.NewRecorder()
			a := &App{}
			requestIDMiddleware(a.requestLog("/checkout", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))).ServeHTTP(rec, req)
			if rec.Code != tc.code {
				t.Fatalf("status %d", rec.Code)
			}
			if strings.Contains(output.String(), "private") {
				t.Fatal("secret leaked into log")
			}
			var log map[string]any
			if err := json.Unmarshal(output.Bytes(), &log); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"trace_id", "span_id", "request_id", "http.method", "http.route", "http.status_code", "duration_ms"} {
				if _, ok := log[key]; !ok {
					t.Fatalf("missing %s", key)
				}
			}
			if tc.name == "valid" && !strings.Contains(output.String(), "visible") {
				t.Fatal("unknown safe field was omitted")
			}
		})
	}
}
