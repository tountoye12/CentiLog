package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type logQueryClient interface {
	Query(context.Context, string, map[string]string) ([]json.RawMessage, error)
}

type clickHouseHTTPClient struct {
	endpoint string
	user     string
	password string
	client   *http.Client
}

func newClickHouseHTTPClient(endpoint, user, password, database string) (*clickHouseHTTPClient, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("invalid ClickHouse HTTP URL")
	}
	query := parsed.Query()
	query.Set("database", database)
	parsed.RawQuery = query.Encode()
	return &clickHouseHTTPClient{
		endpoint: parsed.String(),
		user:     user,
		password: password,
		client:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (client *clickHouseHTTPClient) Query(ctx context.Context, query string, parameters map[string]string) ([]json.RawMessage, error) {
	endpoint, err := url.Parse(client.endpoint)
	if err != nil {
		return nil, errors.New("invalid ClickHouse endpoint")
	}
	queryParameters := endpoint.Query()
	for name, value := range parameters {
		queryParameters.Set("param_"+name, value)
	}
	endpoint.RawQuery = queryParameters.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), strings.NewReader(query))
	if err != nil {
		return nil, errors.New("build ClickHouse query")
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.SetBasicAuth(client.user, client.password)
	response, err := client.client.Do(request)
	if err != nil {
		return nil, errors.New("ClickHouse query unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, errors.New("ClickHouse query failed")
	}

	rows := make([]json.RawMessage, 0)
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 16<<20))
	scanner.Buffer(make([]byte, 64*1024), 2<<20)
	for scanner.Scan() {
		row := json.RawMessage(append([]byte(nil), scanner.Bytes()...))
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("read ClickHouse query results")
	}
	return rows, nil
}
