package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/mail"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const maxJSONBodyBytes = 1 << 20

var errInvalidCredentials = errors.New("invalid credentials")

type principal struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type principalContextKey struct{}

type apiHandler struct {
	repository repository
	clickhouse logQueryClient
	jwt        *jwtManager
	now        func() time.Time
}

func newAPIHandler(repository repository, clickhouse logQueryClient, jwt *jwtManager) http.Handler {
	handler := &apiHandler{repository: repository, clickhouse: clickhouse, jwt: jwt, now: time.Now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("POST /api/login", handler.login)
	mux.Handle("GET /api/me", handler.authenticate(http.HandlerFunc(handler.me)))
	mux.Handle("GET /api/services", handler.authenticate(http.HandlerFunc(handler.listServices)))
	mux.Handle("POST /api/services", handler.requireAdmin(http.HandlerFunc(handler.createService)))
	mux.Handle("PUT /api/services/{name}", handler.requireAdmin(http.HandlerFunc(handler.updateService)))
	mux.Handle("POST /api/users", handler.requireAdmin(http.HandlerFunc(handler.createUser)))
	mux.Handle("GET /api/logs", handler.authenticate(http.HandlerFunc(handler.listLogs)))
	mux.Handle("GET /api/trace/{id}", handler.authenticate(http.HandlerFunc(handler.trace)))
	mux.Handle("GET /api/stats/volume", handler.authenticate(http.HandlerFunc(handler.volumeStats)))
	mux.Handle("GET /api/stats/patterns", handler.authenticate(http.HandlerFunc(handler.patternStats)))
	return mux
}

func (handler *apiHandler) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (handler *apiHandler) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeAPIJSON(w, r, &request) {
		return
	}
	if request.Email == "" || request.Password == "" || len(request.Password) > 72 {
		writeAPIError(w, http.StatusBadRequest, "email and password are required")
		return
	}
	account, err := handler.repository.CredentialsByEmail(r.Context(), request.Email)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(request.Password)) != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	token, expiresAt, err := handler.jwt.Sign(account.user)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not create login token")
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_at":   expiresAt.UTC().Format(time.RFC3339),
	})
}

func (handler *apiHandler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		authorization := r.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, prefix) {
			writeAPIError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		claims, err := handler.jwt.Parse(strings.TrimSpace(strings.TrimPrefix(authorization, prefix)))
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		id, _ := strconv.ParseInt(claims.Subject, 10, 64)
		account := principal{ID: id, Email: claims.Email, Role: claims.Role}
		context := context.WithValue(r.Context(), principalContextKey{}, account)
		next.ServeHTTP(w, r.WithContext(context))
	})
}

func (handler *apiHandler) requireAdmin(next http.Handler) http.Handler {
	return handler.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account := currentPrincipal(r.Context())
		if account.Role != "admin" {
			writeAPIError(w, http.StatusForbidden, "administrator access required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func currentPrincipal(ctx context.Context) principal {
	account, _ := ctx.Value(principalContextKey{}).(principal)
	return account
}

func (handler *apiHandler) me(w http.ResponseWriter, r *http.Request) {
	account := currentPrincipal(r.Context())
	writeAPIJSON(w, http.StatusOK, account)
}

func (handler *apiHandler) listServices(w http.ResponseWriter, r *http.Request) {
	account := currentPrincipal(r.Context())
	services, err := handler.repository.ServicesForUser(r.Context(), account.ID, account.Role)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not list services")
		return
	}
	writeAPIJSON(w, http.StatusOK, services)
}

func (handler *apiHandler) createService(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name         string `json:"name"`
		RetentionDays int   `json:"retention_days"`
	}
	if !decodeAPIJSON(w, r, &request) {
		return
	}
	if strings.TrimSpace(request.Name) == "" || request.RetentionDays < 1 {
		writeAPIError(w, http.StatusBadRequest, "service name and positive retention_days are required")
		return
	}
	apiKey, err := generateAPIKey()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not generate service API key")
		return
	}
	created, err := handler.repository.CreateService(r.Context(), strings.TrimSpace(request.Name), apiKey, request.RetentionDays)
	if err != nil {
		if isUniqueViolation(err) {
			writeAPIError(w, http.StatusConflict, "service name already exists")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "could not create service")
		return
	}
	writeAPIJSON(w, http.StatusCreated, map[string]any{
		"service": created,
		"api_key": apiKey,
		"warning": "This API key is shown only once. Store it securely.",
	})
}

func (handler *apiHandler) updateService(w http.ResponseWriter, r *http.Request) {
	var request struct {
		RetentionDays int `json:"retention_days"`
	}
	if !decodeAPIJSON(w, r, &request) {
		return
	}
	if request.RetentionDays < 1 {
		writeAPIError(w, http.StatusBadRequest, "positive retention_days is required")
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid service name")
		return
	}
	if err := handler.repository.UpdateRetention(r.Context(), name, request.RetentionDays); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIError(w, http.StatusNotFound, "service not found")
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "could not update service")
		return
	}
	writeAPIJSON(w, http.StatusOK, map[string]any{"name": name, "retention_days": request.RetentionDays})
}

func (handler *apiHandler) createUser(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Email    string   `json:"email"`
		Password string   `json:"password"`
		Role     string   `json:"role"`
		Services []string `json:"services"`
	}
	if !decodeAPIJSON(w, r, &request) {
		return
	}
	parsedEmail, err := mail.ParseAddress(request.Email)
	if err != nil || parsedEmail.Address != request.Email || len(request.Password) < 8 || len(request.Password) > 72 {
		writeAPIError(w, http.StatusBadRequest, "valid email and password of 8 to 72 bytes are required")
		return
	}
	if request.Role != "admin" && request.Role != "viewer" {
		writeAPIError(w, http.StatusBadRequest, "role must be admin or viewer")
		return
	}
	if request.Role == "admin" && len(request.Services) > 0 {
		writeAPIError(w, http.StatusBadRequest, "service permissions are only assigned to viewers")
		return
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	created, err := handler.repository.CreateUser(r.Context(), request.Email, string(passwordHash), request.Role, request.Services)
	if err != nil {
		if isUniqueViolation(err) {
			writeAPIError(w, http.StatusConflict, "user email already exists")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "could not create user or service permissions")
		return
	}
	writeAPIJSON(w, http.StatusCreated, created)
}

func (handler *apiHandler) listLogs(w http.ResponseWriter, r *http.Request) {
	account := currentPrincipal(r.Context())
	services, err := handler.permittedServices(r.Context(), account)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not check service permissions")
		return
	}
	allowed := serviceNames(services)
	if requested := r.URL.Query().Get("service"); requested != "" {
		if !containsService(allowed, requested) {
			writeAPIError(w, http.StatusForbidden, "service access denied")
			return
		}
		allowed = []string{requested}
	}
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 1000 {
			writeAPIError(w, http.StatusBadRequest, "limit must be between 1 and 1000")
			return
		}
		limit = parsed
	}
	params := map[string]string{
		"allowed_services": jsonStringArray(allowed),
		"has_from":         "0",
		"from_ms":          "0",
		"has_to":           "0",
		"to_ms":            "0",
		"has_before":       "0",
		"before_ms":        "0",
		"level":            "",
		"text":             "",
		"trace_id":         "",
		"limit":            strconv.Itoa(limit),
	}
	for _, bound := range []struct {
		name string
		flag string
		param string
	}{
		{name: "from", flag: "has_from", param: "from_ms"},
		{name: "to", flag: "has_to", param: "to_ms"},
		{name: "before", flag: "has_before", param: "before_ms"},
	} {
		if value, present := r.URL.Query()[bound.name]; present {
			if len(value) != 1 {
				writeAPIError(w, http.StatusBadRequest, bound.name+" must be provided once")
				return
			}
			parsed, err := strconv.ParseInt(value[0], 10, 64)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, bound.name+" must be milliseconds")
				return
			}
			params[bound.flag] = "1"
			params[bound.param] = strconv.FormatInt(parsed, 10)
		}
	}
	if params["has_from"] == "1" && params["has_to"] == "1" {
		from, _ := strconv.ParseInt(params["from_ms"], 10, 64)
		to, _ := strconv.ParseInt(params["to_ms"], 10, 64)
		if from > to {
			writeAPIError(w, http.StatusBadRequest, "from must not be after to")
			return
		}
	}
	level := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("level")))
	if level != "" && !validLogLevel(level) {
		writeAPIError(w, http.StatusBadRequest, "level is not supported")
		return
	}
	text := r.URL.Query().Get("text")
	traceID := r.URL.Query().Get("trace_id")
	if len(text) > 1024 || len(traceID) > 512 {
		writeAPIError(w, http.StatusBadRequest, "text or trace_id is too long")
		return
	}
	params["level"] = level
	params["text"] = text
	params["trace_id"] = traceID
	const query = `SELECT timestamp, service, host, env, level, message, trace_id, attributes, redacted, ingested_at, retention_days
FROM centilog.logs
WHERE has(JSONExtract({allowed_services:String}, 'Array(String)'), service)
  AND ({has_from:UInt8} = 0 OR timestamp >= fromUnixTimestamp64Milli({from_ms:Int64}))
  AND ({has_to:UInt8} = 0 OR timestamp <= fromUnixTimestamp64Milli({to_ms:Int64}))
  AND ({has_before:UInt8} = 0 OR timestamp < fromUnixTimestamp64Milli({before_ms:Int64}))
  AND ({level:String} = '' OR level = {level:String})
  AND ({text:String} = '' OR positionCaseInsensitive(message, {text:String}) > 0)
  AND ({trace_id:String} = '' OR trace_id = {trace_id:String})
ORDER BY timestamp DESC
LIMIT {limit:UInt16}
FORMAT JSONEachRow`
	rows, err := handler.clickhouse.Query(r.Context(), query, params)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "log query unavailable")
		return
	}
	if rows == nil {
		rows = make([]json.RawMessage, 0)
	}
	writeAPIJSON(w, http.StatusOK, rows)
}

func (handler *apiHandler) trace(w http.ResponseWriter, r *http.Request) {
	traceID := r.PathValue("id")
	if traceID == "" || len(traceID) > 512 {
		writeAPIError(w, http.StatusBadRequest, "invalid trace id")
		return
	}
	account := currentPrincipal(r.Context())
	services, err := handler.permittedServices(r.Context(), account)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not check service permissions")
		return
	}
	params := map[string]string{
		"allowed_services": jsonStringArray(serviceNames(services)),
		"trace_id":         traceID,
		"from_ms":          strconv.FormatInt(handler.now().UTC().Add(-7*24*time.Hour).UnixMilli(), 10),
		"to_ms":            strconv.FormatInt(handler.now().UTC().UnixMilli(), 10),
	}
	const query = `SELECT timestamp, service, host, env, level, message, trace_id, attributes, redacted, ingested_at, retention_days
FROM centilog.logs
WHERE has(JSONExtract({allowed_services:String}, 'Array(String)'), service)
  AND trace_id = {trace_id:String}
  AND timestamp >= fromUnixTimestamp64Milli({from_ms:Int64})
  AND timestamp <= fromUnixTimestamp64Milli({to_ms:Int64})
ORDER BY timestamp ASC
FORMAT JSONEachRow`
	rows, err := handler.clickhouse.Query(r.Context(), query, params)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "trace query unavailable")
		return
	}
	if rows == nil {
		rows = make([]json.RawMessage, 0)
	}
	writeAPIJSON(w, http.StatusOK, rows)
}

func (handler *apiHandler) volumeStats(w http.ResponseWriter, r *http.Request) {
	days := 7
	if value := r.URL.Query().Get("days"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 365 {
			writeAPIError(w, http.StatusBadRequest, "days must be between 1 and 365")
			return
		}
		days = parsed
	}
	account := currentPrincipal(r.Context())
	services, err := handler.permittedServices(r.Context(), account)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not check service permissions")
		return
	}
	params := map[string]string{
		"allowed_services": jsonStringArray(serviceNames(services)),
		"from_ms":          strconv.FormatInt(handler.now().UTC().AddDate(0, 0, -days).UnixMilli(), 10),
		"to_ms":            strconv.FormatInt(handler.now().UTC().UnixMilli(), 10),
	}
	const query = `SELECT toDate(timestamp) AS day, service, count() AS log_count,
       sum(length(toJSONString(tuple(timestamp, service, host, env, level, message, trace_id, attributes, redacted, ingested_at, retention_days)))) AS raw_bytes
FROM centilog.logs
WHERE has(JSONExtract({allowed_services:String}, 'Array(String)'), service)
  AND timestamp >= fromUnixTimestamp64Milli({from_ms:Int64})
	AND timestamp <= fromUnixTimestamp64Milli({to_ms:Int64})
GROUP BY day, service
ORDER BY day DESC, service ASC
FORMAT JSONEachRow`
	rows, err := handler.clickhouse.Query(r.Context(), query, params)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "volume query unavailable")
		return
	}
	if rows == nil {
		rows = make([]json.RawMessage, 0)
	}
	writeAPIJSON(w, http.StatusOK, rows)
}

func (handler *apiHandler) patternStats(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if value := r.URL.Query().Get("hours"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 720 {
			writeAPIError(w, http.StatusBadRequest, "hours must be between 1 and 720")
			return
		}
		hours = parsed
	}
	account := currentPrincipal(r.Context())
	services, err := handler.permittedServices(r.Context(), account)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "could not check service permissions")
		return
	}
	params := map[string]string{
		"allowed_services": jsonStringArray(serviceNames(services)),
		"from_ms":          strconv.FormatInt(handler.now().UTC().Add(-time.Duration(hours)*time.Hour).UnixMilli(), 10),
		"to_ms":            strconv.FormatInt(handler.now().UTC().UnixMilli(), 10),
	}
	const query = `SELECT pattern, count() AS log_count, max(timestamp) AS last_seen
FROM
(
    SELECT replaceRegexpAll(replaceRegexpAll(message, '[0-9A-Fa-f]{16,}', 'ID'), '[0-9]+', 'N') AS pattern,
           timestamp, service
    FROM centilog.logs
    WHERE has(JSONExtract({allowed_services:String}, 'Array(String)'), service)
      AND timestamp >= fromUnixTimestamp64Milli({from_ms:Int64})
			AND timestamp <= fromUnixTimestamp64Milli({to_ms:Int64})
)
GROUP BY pattern
ORDER BY log_count DESC, last_seen DESC
LIMIT 50
FORMAT JSONEachRow`
	rows, err := handler.clickhouse.Query(r.Context(), query, params)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "pattern query unavailable")
		return
	}
	if rows == nil {
		rows = make([]json.RawMessage, 0)
	}
	writeAPIJSON(w, http.StatusOK, rows)
}

func (handler *apiHandler) permittedServices(ctx context.Context, account principal) ([]service, error) {
	return handler.repository.ServicesForUser(ctx, account.ID, account.Role)
}

func serviceNames(services []service) []string {
	names := make([]string, 0, len(services))
	for _, item := range services {
		names = append(names, item.Name)
	}
	return names
}

func jsonStringArray(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func containsService(services []string, requested string) bool {
	for _, name := range services {
		if name == requested {
			return true
		}
	}
	return false
}

func validLogLevel(level string) bool {
	switch level {
	case "trace", "debug", "info", "warn", "error", "fatal":
		return true
	default:
		return false
	}
}

func generateAPIKey() (string, error) {
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return "", errors.New("generate API key")
	}
	return hex.EncodeToString(key[:]), nil
}

func decodeAPIJSON(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid JSON request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "request must contain one JSON object")
		return false
	}
	return true
}

func writeAPIJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeAPIJSON(w, status, map[string]string{"error": message})
}
