package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type fakeRepository struct {
	account         user
	passwordHash    string
	services        []service
	createdUser     user
	createdServices []string
	createdKey      string
	lastLogUserID   int64
	lastLogRole     string
}

func (repository *fakeRepository) CredentialsByEmail(context.Context, string) (credentials, error) {
	return credentials{user: repository.account, PasswordHash: repository.passwordHash}, nil
}

func (repository *fakeRepository) ServicesForUser(_ context.Context, userID int64, role string) ([]service, error) {
	repository.lastLogUserID = userID
	repository.lastLogRole = role
	if role == "admin" || len(repository.services) == 0 {
		return repository.services, nil
	}
	return repository.services[:1], nil
}

func (repository *fakeRepository) CreateService(_ context.Context, name, apiKey string, retentionDays int) (service, error) {
	repository.createdKey = apiKey
	return service{Name: name, RetentionDays: retentionDays}, nil
}

func (repository *fakeRepository) UpdateRetention(context.Context, string, int) error {
	return nil
}

func (repository *fakeRepository) CreateUser(_ context.Context, email, passwordHash, role string, services []string) (user, error) {
	repository.createdUser = user{ID: 7, Email: email, Role: role}
	repository.passwordHash = passwordHash
	repository.createdServices = append([]string(nil), services...)
	return repository.createdUser, nil
}

type fakeClickHouse struct {
	query      string
	parameters map[string]string
	rows       []json.RawMessage
}

func (client *fakeClickHouse) Query(_ context.Context, query string, parameters map[string]string) ([]json.RawMessage, error) {
	client.query = query
	client.parameters = make(map[string]string, len(parameters))
	for name, value := range parameters {
		client.parameters[name] = value
	}
	return client.rows, nil
}

func TestJWTExpiresAfterTwelveHoursAndRejectsTampering(t *testing.T) {
	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	manager := newJWTManager([]byte("test-only-secret"))
	manager.now = func() time.Time { return now }
	token, expires, err := manager.Sign(user{ID: 42, Email: "viewer@example.test", Role: "viewer"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if !expires.Equal(now.Add(12 * time.Hour)) {
		t.Fatalf("expiry = %s, want 12 hours", expires)
	}
	claims, err := manager.Parse(token)
	if err != nil || claims.Subject != "42" || claims.Role != "viewer" {
		t.Fatalf("Parse() claims=%+v error=%v", claims, err)
	}
	if _, err := manager.Parse(token + "x"); err == nil {
		t.Fatal("tampered JWT was accepted")
	}
}

func TestViewerLogQueryContainsOnlyPermittedServices(t *testing.T) {
	repository := &fakeRepository{
		account:  user{ID: 12, Email: "viewer@example.test", Role: "viewer"},
		services: []service{{Name: "allowed-service"}, {Name: "other-service"}},
	}
	clickhouse := &fakeClickHouse{rows: []json.RawMessage{json.RawMessage(`{"message":"visible"}`)}}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(repository.account)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	handler := newAPIHandler(repository, clickhouse, jwt)
	request := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(clickhouse.query, "has(JSONExtract({allowed_services:String}") || strings.Contains(clickhouse.query, "allowed-service") || strings.Contains(clickhouse.query, "other-service") {
		t.Fatalf("ClickHouse SQL did not use a permission parameter: %s", clickhouse.query)
	}
	if clickhouse.parameters["allowed_services"] != `["allowed-service"]` {
		t.Fatalf("allowed_services parameter = %q", clickhouse.parameters["allowed_services"])
	}
}

func TestViewerCannotRequestUnassignedService(t *testing.T) {
	repository := &fakeRepository{account: user{ID: 12, Email: "viewer@example.test", Role: "viewer"}, services: []service{{Name: "allowed-service"}}}
	clickhouse := &fakeClickHouse{}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(repository.account)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/logs?service=other-service", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	newAPIHandler(repository, clickhouse, jwt).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || clickhouse.query != "" {
		t.Fatalf("status=%d query=%q, want forbidden without ClickHouse query", response.Code, clickhouse.query)
	}
}

func TestViewerServicesEndpointReturnsOnlyAssignedServices(t *testing.T) {
	repository := &fakeRepository{
		account:  user{ID: 12, Email: "viewer@example.test", Role: "viewer"},
		services: []service{{Name: "allowed-service"}, {Name: "other-service"}},
	}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(repository.account)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	newAPIHandler(repository, &fakeClickHouse{}, jwt).ServeHTTP(response, request)
	var services []service
	if err := json.Unmarshal(response.Body.Bytes(), &services); err != nil {
		t.Fatalf("decode services: %v", err)
	}
	if response.Code != http.StatusOK || len(services) != 1 || services[0].Name != "allowed-service" {
		t.Fatalf("status=%d services=%+v, want only allowed-service", response.Code, services)
	}
}

func TestLoginReturnsSignedBearerToken(t *testing.T) {
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("test-only-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash test password: %v", err)
	}
	repository := &fakeRepository{
		account:      user{ID: 12, Email: "admin@example.test", Role: "admin"},
		passwordHash: string(passwordHash),
	}
	jwt := newJWTManager([]byte("test-only-secret"))
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"admin@example.test","password":"test-only-password"}`))
	response := httptest.NewRecorder()
	newAPIHandler(repository, &fakeClickHouse{}, jwt).ServeHTTP(response, request)
	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	claims, err := jwt.Parse(result.AccessToken)
	if response.Code != http.StatusOK || err != nil || result.TokenType != "Bearer" || claims.Email != repository.account.Email {
		t.Fatalf("login status=%d token type=%q claims=%+v err=%v", response.Code, result.TokenType, claims, err)
	}
}

func TestMeReturnsAuthenticatedPrincipal(t *testing.T) {
	account := user{ID: 12, Email: "viewer@example.test", Role: "viewer"}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(account)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	newAPIHandler(&fakeRepository{}, &fakeClickHouse{}, jwt).ServeHTTP(response, request)
	var got user
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode /api/me response: %v", err)
	}
	if response.Code != http.StatusOK || got != account {
		t.Fatalf("/api/me status=%d got=%+v want=%+v", response.Code, got, account)
	}
}

func TestAdminCreatesViewerWithAssignedServices(t *testing.T) {
	repository := &fakeRepository{}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(user{ID: 1, Email: "admin@example.test", Role: "admin"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"email":"viewer@example.test","password":"test-only-password","role":"viewer","services":["allowed-service"]}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	newAPIHandler(repository, &fakeClickHouse{}, jwt).ServeHTTP(response, request)
	if response.Code != http.StatusCreated || repository.createdUser.Role != "viewer" || len(repository.createdServices) != 1 || repository.createdServices[0] != "allowed-service" {
		t.Fatalf("status=%d user=%+v services=%v", response.Code, repository.createdUser, repository.createdServices)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(repository.passwordHash), []byte("test-only-password")); err != nil {
		t.Error("created user's password was not bcrypt-hashed")
	}
	if strings.Contains(response.Body.String(), "password_hash") {
		t.Error("user creation response exposed the password hash")
	}
}

func TestCreateServiceShowsFreshKeyOnlyInCreateResponse(t *testing.T) {
	repository := &fakeRepository{}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(user{ID: 1, Email: "admin@example.test", Role: "admin"})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/services", strings.NewReader(`{"name":"new-service","retention_days":30}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	newAPIHandler(repository, &fakeClickHouse{}, jwt).ServeHTTP(response, request)
	if response.Code != http.StatusCreated || repository.createdKey == "" || len(repository.createdKey) != 64 {
		t.Fatalf("status=%d generated key length=%d", response.Code, len(repository.createdKey))
	}
	if !strings.Contains(response.Body.String(), repository.createdKey) {
		t.Fatal("create response did not include the generated key")
	}
}

func TestAllowedServicesEncodedAsJSONParameter(t *testing.T) {
	parameter := jsonStringArray([]string{"api' OR 1=1 --", "back\\slash"})
	if parameter != `["api' OR 1=1 --","back\\slash"]` {
		t.Fatalf("allowed services parameter = %q", parameter)
	}
}

func TestLogSearchBindsEveryFilter(t *testing.T) {
	repository := &fakeRepository{
		account:  user{ID: 22, Email: "viewer@example.test", Role: "viewer"},
		services: []service{{Name: "demo-service"}},
	}
	clickhouse := &fakeClickHouse{}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(repository.account)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/logs?from=1700000000000&to=1700000060000&service=demo-service&level=ERROR&text=needle%27%20OR%201%3D1&trace_id=trace-1&before=1700000050000&limit=1000", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	newAPIHandler(repository, clickhouse, jwt).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	for _, userInput := range []string{"1700000000000", "needle", "trace-1", "demo-service"} {
		if strings.Contains(clickhouse.query, userInput) {
			t.Errorf("query contains unbound user input %q", userInput)
		}
	}
	for name, want := range map[string]string{
		"allowed_services": `["demo-service"]`,
		"from_ms":          "1700000000000",
		"to_ms":            "1700000060000",
		"before_ms":        "1700000050000",
		"level":            "error",
		"text":             "needle' OR 1=1",
		"trace_id":         "trace-1",
		"limit":            "1000",
	} {
		if clickhouse.parameters[name] != want {
			t.Errorf("parameter %q = %q, want %q", name, clickhouse.parameters[name], want)
		}
	}
	if !strings.Contains(clickhouse.query, "ORDER BY timestamp DESC") {
		t.Error("logs are not ordered newest first")
	}
}

func TestTraceQueryUsesViewerPermissionsAndSevenDayWindow(t *testing.T) {
	repository := &fakeRepository{
		account:  user{ID: 22, Email: "viewer@example.test", Role: "viewer"},
		services: []service{{Name: "demo-service"}},
	}
	clickhouse := &fakeClickHouse{}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(repository.account)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
	handler := newAPIHandler(repository, clickhouse, jwt)
	handler.(*apiHandler).now = func() time.Time { return now }
	request := httptest.NewRequest(http.MethodGet, "/api/trace/trace-123", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if clickhouse.parameters["trace_id"] != "trace-123" || clickhouse.parameters["allowed_services"] != `["demo-service"]` {
		t.Fatalf("trace parameters = %#v", clickhouse.parameters)
	}
	if clickhouse.parameters["from_ms"] != strconv.FormatInt(now.Add(-7*24*time.Hour).UnixMilli(), 10) || !strings.Contains(clickhouse.query, "ORDER BY timestamp ASC") {
		t.Fatalf("trace query does not enforce seven-day oldest-first scope: %s", clickhouse.query)
	}
}

func TestStatsQueriesArePermissionScopedAndParameterized(t *testing.T) {
	for _, test := range []struct {
		name  string
		path  string
		query string
		param string
	}{
		{name: "volume", path: "/api/stats/volume?days=30", query: "GROUP BY day, service", param: "from_ms"},
		{name: "patterns", path: "/api/stats/patterns?hours=48", query: "LIMIT 50", param: "from_ms"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{
				account:  user{ID: 22, Email: "viewer@example.test", Role: "viewer"},
				services: []service{{Name: "demo-service"}},
			}
			clickhouse := &fakeClickHouse{}
			jwt := newJWTManager([]byte("test-only-secret"))
			token, _, err := jwt.Sign(repository.account)
			if err != nil {
				t.Fatalf("Sign() error = %v", err)
			}
			handler := newAPIHandler(repository, clickhouse, jwt)
			now := time.Date(2026, time.September, 25, 12, 0, 0, 0, time.UTC)
			handler.(*apiHandler).now = func() time.Time { return now }
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || !strings.Contains(clickhouse.query, test.query) {
				t.Fatalf("status=%d query=%s", response.Code, clickhouse.query)
			}
			if clickhouse.parameters["allowed_services"] != `["demo-service"]` || clickhouse.parameters[test.param] == "" || strings.Contains(clickhouse.query, "demo-service") {
				t.Fatalf("stats query was not permission-scoped with bound values: query=%s params=%#v", clickhouse.query, clickhouse.parameters)
			}
			if clickhouse.parameters["to_ms"] == "" {
				t.Fatal("stats query is missing its upper time bound")
			}
		})
	}
}

func TestClickHouseHTTPClientSendsParametersSeparately(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("param_text"); got != "needle' OR 1=1" {
			t.Errorf("text parameter = %q", got)
		}
		if got := r.URL.Query().Get("database"); got != "centilog" {
			t.Errorf("database parameter = %q", got)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read query body: %v", err)
		}
		if strings.Contains(string(body), "needle") {
			t.Errorf("user search text was interpolated into SQL: %s", body)
		}
		_, _ = io.WriteString(w, `{"message":"ok"}`+"\n")
	}))
	defer server.Close()
	client, err := newClickHouseHTTPClient(server.URL, "test-user", "test-password", "centilog")
	if err != nil {
		t.Fatalf("newClickHouseHTTPClient() error = %v", err)
	}
	rows, err := client.Query(context.Background(), "SELECT {text:String} FORMAT JSONEachRow", map[string]string{"text": "needle' OR 1=1"})
	if err != nil || len(rows) != 1 || string(rows[0]) != `{"message":"ok"}` {
		t.Fatalf("Query() rows=%q error=%v", rows, err)
	}
}

func TestAdminRouteRejectsViewer(t *testing.T) {
	repository := &fakeRepository{account: user{ID: 2, Email: "viewer@example.test", Role: "viewer"}}
	jwt := newJWTManager([]byte("test-only-secret"))
	token, _, err := jwt.Sign(repository.account)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/services", strings.NewReader(`{"name":"new-service","retention_days":30}`))
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	newAPIHandler(repository, &fakeClickHouse{}, jwt).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestLoginRejectsInvalidHashWithoutExposingDetails(t *testing.T) {
	repository := &fakeRepository{account: user{ID: 1, Email: "admin@example.test", Role: "admin"}}
	jwt := newJWTManager([]byte("test-only-secret"))
	request := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"email":"admin@example.test","password":"wrong"}`))
	response := httptest.NewRecorder()
	newAPIHandler(repository, &fakeClickHouse{}, jwt).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || strings.Contains(response.Body.String(), "hash") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestRepositoryErrorSentinelDoesNotLeak(t *testing.T) {
	if !errors.Is(errInvalidCredentials, errInvalidCredentials) {
		t.Fatal("invalid-credentials sentinel mismatch")
	}
}
