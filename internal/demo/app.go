package demo

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/XSAM/otelsql"
	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type Checkout struct {
	UserID    int64 `json:"user_id"`
	ProductID int64 `json:"product_id"`
	Quantity  int64 `json:"quantity"`
	Price     int64 `json:"price,omitempty"`
}
type Product struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Price int64  `json:"price"`
	Stock int64  `json:"stock"`
}
type Result struct {
	Success bool   `json:"success"`
	OrderID int64  `json:"order_id,omitempty"`
	TraceID string `json:"trace_id"`
	Error   string `json:"error,omitempty"`
}
type App struct {
	Service              string
	DB                   *sql.DB
	Redis                *redis.Client
	Client               *http.Client
	ProductURL, OrderURL string
	// ponytail: one Cart Service replica; use a Redis transaction if Cart Service is scaled out.
	cartMu  sync.Mutex
	metrics *appMetrics
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func validCheckout(c Checkout) bool {
	return c.UserID > 0 && c.ProductID > 0 && c.Quantity > 0 && c.Quantity <= 1000000
}
func total(c Checkout) (int64, error) {
	if !validCheckout(c) || c.Price <= 0 || c.Price > math.MaxInt64/c.Quantity {
		return 0, errors.New("INVALID_ORDER")
	}
	return c.Price * c.Quantity, nil
}
func respond(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, r *http.Request, code int, reason string) {
	recordFailure(r.Context(), reason)
	if data, ok := r.Context().Value(requestDataKey{}).(*requestData); ok {
		data.errorType = reason
	}
	respond(w, code, Result{Error: reason, TraceID: traceID(r.Context())})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		fail(w, r, 400, "INVALID_BODY")
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, r, 400, "INVALID_BODY")
		return false
	}
	return true
}
func (a *App) call(ctx context.Context, method, url string, body any, out any) (int, error) {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", rand.Text())
	query := req.URL.Query()
	query.Set("scenario", scenario(ctx))
	req.URL.RawQuery = query.Encode()
	res, err := a.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode, errors.New("DOWNSTREAM_ERROR")
	}
	return res.StatusCode, json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
}
func (a *App) checkout(w http.ResponseWriter, r *http.Request) {
	var c Checkout
	if !decode(w, r, &c) {
		return
	}
	if !validCheckout(c) {
		fail(w, r, 400, "INVALID_CHECKOUT")
		return
	}
	c.Price = 0
	stored, err := a.cart(r.Context(), c)
	if err != nil {
		fail(w, r, 503, "REDIS_ERROR")
		return
	}
	c = stored
	var p Product
	if status, callErr := a.call(r.Context(), "GET", fmt.Sprintf("%s/products/%d", a.ProductURL, c.ProductID), nil, &p); callErr != nil {
		if status == 404 {
			fail(w, r, 404, "PRODUCT_NOT_FOUND")
			return
		}
		fail(w, r, 502, "PRODUCT_ERROR")
		return
	}
	if c.Quantity > p.Stock {
		fail(w, r, 409, "OUT_OF_STOCK")
		return
	}
	c.Price = p.Price
	var result Result
	if _, err = a.call(r.Context(), "POST", a.OrderURL+"/orders", c, &result); err != nil {
		fail(w, r, 502, "ORDER_ERROR")
		return
	}
	result.TraceID = traceID(r.Context())
	respond(w, 200, result)
}
func (a *App) cart(ctx context.Context, c Checkout) (Checkout, error) {
	a.cartMu.Lock()
	defer a.cartMu.Unlock()
	data, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	key := fmt.Sprintf("cart:%d", c.UserID)
	if err = a.Redis.Set(ctx, key, data, time.Hour).Err(); err != nil {
		return c, err
	}
	raw, err := a.Redis.Get(ctx, key).Bytes()
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(raw, &c)
	return c, err
}
func (a *App) product(w http.ResponseWriter, r *http.Request) {
	if scenario(r.Context()) == "product-error" {
		fail(w, r, 500, "PRODUCT_UNAVAILABLE")
		return
	}
	if scenario(r.Context()) == "slow-product" {
		if err := delay(r.Context(), 1500*time.Millisecond); err != nil {
			fail(w, r, 504, "REQUEST_TIMEOUT")
			return
		}
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, r, 400, "INVALID_PRODUCT")
		return
	}
	var p Product
	err = a.DB.QueryRowContext(r.Context(), "SELECT id, name, price, stock FROM products WHERE id = ?", id).Scan(&p.ID, &p.Name, &p.Price, &p.Stock)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, r, 404, "PRODUCT_NOT_FOUND")
		return
	}
	if err != nil {
		fail(w, r, 500, "DATABASE_ERROR")
		return
	}
	if scenario(r.Context()) == "out-of-stock" {
		p.Stock = 0
	}
	respond(w, 200, p)
}
func (a *App) order(w http.ResponseWriter, r *http.Request) {
	var c Checkout
	if !decode(w, r, &c) {
		return
	}
	amount, err := total(c)
	if err != nil {
		fail(w, r, 400, "INVALID_ORDER")
		return
	}
	if scenario(r.Context()) == "slow-db" {
		var slept int
		if err := a.DB.QueryRowContext(r.Context(), "SELECT SLEEP(1.5)").Scan(&slept); err != nil {
			fail(w, r, 500, "DATABASE_ERROR")
			return
		}
	}
	productID := c.ProductID
	if scenario(r.Context()) == "order-error" {
		// An intentional foreign key violation rejects the INSERT without storing an Order.
		productID = -1
	}
	result, err := a.DB.ExecContext(r.Context(), "INSERT INTO orders (user_id, product_id, quantity, price, total_amount, status) VALUES (?, ?, ?, ?, ?, 'created')", c.UserID, productID, c.Quantity, c.Price, amount)
	if err != nil {
		fail(w, r, 500, "ORDER_CREATION_FAILED")
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		fail(w, r, 500, "DATABASE_ERROR")
		return
	}
	respond(w, 201, Result{Success: true, OrderID: id, TraceID: traceID(r.Context())})
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	switch a.Service {
	case "cart-service":
		mux.Handle("POST /checkout", a.route("/checkout", a.checkout))
	case "product-service":
		mux.Handle("GET /products/{id}", a.route("/products/{id}", a.product))
	case "order-service":
		mux.Handle("POST /orders", a.route("/orders", a.order))
	}
	return requestIDMiddleware(mux)
}
func Run(service string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown, err := startTelemetry(ctx, service)
	if err != nil {
		panic(err)
	}
	defer shutdown()
	a := &App{Service: service, Client: &http.Client{Timeout: 8 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport)}, ProductURL: env("PRODUCT_URL", "http://product-service:8080"), OrderURL: env("ORDER_URL", "http://order-service:8080")}
	a.metrics, err = newMetrics(otel.Meter("checkout-demo"))
	if err != nil {
		panic(err)
	}
	if service == "cart-service" {
		a.Redis = redis.NewClient(&redis.Options{Addr: env("REDIS_ADDR", "redis:6379"), MaxRetries: -1, ContextTimeoutEnabled: true})
		defer a.Redis.Close()
		if err := redisotel.InstrumentMetrics(a.Redis, redisotel.WithSemConvCompliantMetrics(true)); err != nil {
			panic(err)
		}
		if err := redisotel.InstrumentTracing(a.Redis, redisotel.WithDBStatement(false), redisotel.WithCallerEnabled(false)); err != nil {
			panic(err)
		}
		a.Redis.AddHook(redisDelayHook{})
	} else {
		var err error
		a.DB, err = otelsql.Open("mysql", env("MYSQL_DSN", "demo:demo-local@tcp(mysql:3306)/checkout?parseTime=true&timeout=5s&readTimeout=5s&writeTimeout=5s"), otelsql.WithAttributes(attribute.String("db.system.name", "mysql"), attribute.String("db.system", "mysql")), otelsql.WithSpanOptions(otelsql.SpanOptions{DisableErrSkip: true}))
		if err != nil {
			panic(err)
		}
		defer a.DB.Close()
		a.DB.SetMaxOpenConns(5)
		a.DB.SetMaxIdleConns(2)
		registration, err := otelsql.RegisterDBStatsMetrics(a.DB, otelsql.WithAttributes(attribute.String("db.system.name", "mysql")))
		if err != nil {
			panic(err)
		}
		defer registration.Unregister()
	}
	srv := &http.Server{Addr: ":8080", Handler: a.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("service started")
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server failed")
		os.Exit(1)
	}
	<-stopped
}
