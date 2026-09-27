package demo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/otel/trace"
)

const maxBodyBytes = 16 << 10

type requestIDKey struct{}
type requestDataKey struct{}
type requestData struct {
	body      any
	errorType string
}

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !validRequestID.MatchString(id) {
			id = rand.Text()
		}
		r.Header.Set("X-Request-Id", id)
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}
func requestID(ctx context.Context) string { id, _ := ctx.Value(requestIDKey{}).(string); return id }
func safeHeaders(headers http.Header) map[string]string {
	out := make(map[string]string)
	for _, key := range []string{"Content-Type", "Accept", "User-Agent", "X-Request-Id", "X-Client-Version"} {
		if value := headers.Get(key); value != "" {
			out[strings.ToLower(key)] = value
		}
	}
	return out
}
func sensitiveField(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == ' ' || r == '.' {
			return -1
		}
		return r
	}, strings.ToLower(key))
	for _, part := range []string{"password", "passwd", "token", "authorization", "apikey", "creditcard", "cardnumber", "cvv", "cookie", "session", "secret", "jwt"} {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return false
}
func redact(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if number, err := typed.Int64(); err == nil {
			return number
		}
		// Preserve other numbers as text: OTLP cannot represent every JSON number exactly.
		return typed.String()
	case map[string]any:
		for key, value := range typed {
			if sensitiveField(key) {
				typed[key] = "[REDACTED]"
			} else {
				typed[key] = redact(value)
			}
		}
	case []any:
		for i, value := range typed {
			typed[i] = redact(value)
		}
	}
	return value
}
func sanitizedBody(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("INVALID_BODY")
	}
	return redact(value), nil
}
func (a *App) requestLog(route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := &requestData{}
		r = r.WithContext(context.WithValue(r.Context(), requestDataKey{}, data))
		metrics := httpsnoop.CaptureMetrics(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || contentType != "application/json" {
					fail(w, r, 415, "UNSUPPORTED_CONTENT_TYPE")
					return
				}
				raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
				if err != nil {
					data.body = "[OMITTED: unreadable body]"
					var tooLarge *http.MaxBytesError
					if errors.As(err, &tooLarge) {
						fail(w, r, 413, "BODY_TOO_LARGE")
					} else {
						fail(w, r, 400, "INVALID_BODY")
					}
					return
				}
				data.body, err = sanitizedBody(raw)
				if err != nil {
					data.body = "[OMITTED: invalid JSON]"
					fail(w, r, 400, "INVALID_BODY")
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
			next.ServeHTTP(w, r)
		}), w, r)
		if a.metrics != nil {
			a.metrics.record(r.Context(), r.Method, route, metrics.Code, metrics.Duration)
		}
		sc := trace.SpanContextFromContext(r.Context())
		spanID := ""
		if sc.IsValid() {
			spanID = sc.SpanID().String()
		}
		level := slog.LevelInfo
		if metrics.Code >= 500 {
			level = slog.LevelError
		} else if metrics.Code >= 400 {
			level = slog.LevelWarn
		}
		attrs := []slog.Attr{
			slog.String("trace_id", traceID(r.Context())), slog.String("span_id", spanID), slog.String("request_id", requestID(r.Context())),
			slog.String("demo.scenario", scenario(r.Context())),
			slog.String("http.method", r.Method), slog.String("http.route", route), slog.Int("http.status_code", metrics.Code),
			slog.Float64("duration_ms", float64(metrics.Duration)/float64(time.Millisecond)), slog.Any("request.headers", safeHeaders(r.Header)),
		}
		if data.body != nil {
			attrs = append(attrs, slog.Any("request.body", data.body))
		}
		if data.errorType != "" {
			attrs = append(attrs, slog.String("error.type", data.errorType), slog.String("error.message", data.errorType))
		}
		slog.LogAttrs(r.Context(), level, "HTTP request completed", attrs...)
	})
}
