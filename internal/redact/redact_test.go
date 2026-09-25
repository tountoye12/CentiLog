package redact

import (
	"testing"

	"centilog/internal/schema"
)

func TestRedactMessages(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		want      string
		redacted  bool
	}{
		{name: "password assignment", message: "password=hunter2", want: "password=[REDACTED]", redacted: true},
		{name: "password with spaces", message: "password=two words here, status=ok", want: "password=[REDACTED], status=ok", redacted: true},
		{name: "secret inside JSON", message: `{"user":{"client_secret":"fake secret value"}}`, want: `{"user":{"client_secret":"[REDACTED]"}}`, redacted: true},
		{name: "secret key", message: "secret_key=fake-key", want: "secret_key=[REDACTED]", redacted: true},
		{name: "bearer token", message: "request used Bearer abc.def-ghi", want: "request used Bearer [REDACTED]", redacted: true},
		{name: "API key", message: "api_key=sk_test_example", want: "api_key=[REDACTED]", redacted: true},
		{name: "API token", message: "api token: fake-token", want: "api token: [REDACTED]", redacted: true},
		{name: "AWS access key", message: "key=AKIAIOSFODNN7EXAMPLE", want: "key=[REDACTED]", redacted: true},
		{name: "email address", message: "sent to user@example.test", want: "sent to [REDACTED]", redacted: true},
		{name: "Luhn-valid card with separators", message: "card 4111 1111 1111 1111", want: "card [REDACTED]", redacted: true},
		{name: "card-shaped number failing Luhn", message: "order 4111111111111112", want: "order 4111111111111112", redacted: false},
		{name: "clean message", message: "request completed successfully", want: "request completed successfully", redacted: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Redact(schema.Log{Message: test.message})
			if got.Message != test.want {
				t.Errorf("message was not redacted as expected")
			}
			if got.Redacted != test.redacted {
				t.Errorf("Redacted = %t, want %t", got.Redacted, test.redacted)
			}
		})
	}
}

func TestRedactSensitiveAttributeValues(t *testing.T) {
	attributes := map[string]string{
		"password":       "fake-password",
		"api_token":      "fake-token",
		"clientSecret":   "fake-secret",
		"session_id":     "fake-session",
		"jwt":            "fake-jwt",
		"Authorization":  "Bearer fake-token",
		"request_cookie": "session=fake-cookie",
		"author":         "Mamadou",
	}
	log := Redact(schema.Log{Attributes: attributes})

	for _, key := range []string{"password", "api_token", "clientSecret", "session_id", "jwt", "Authorization", "request_cookie"} {
		if log.Attributes[key] != Mask {
			t.Errorf("sensitive attribute %q was not fully masked", key)
		}
	}
	if log.Attributes["author"] != "Mamadou" {
		t.Error("non-sensitive author attribute changed")
	}
	if !log.Redacted {
		t.Error("Redacted = false, want true when attributes change")
	}
	if attributes["password"] != "fake-password" {
		t.Error("Redact modified the caller's original attribute map")
	}
}

func TestRedactScansNonSensitiveAttributeValues(t *testing.T) {
	log := Redact(schema.Log{Attributes: map[string]string{"request": "contact user@example.test"}})
	if log.Attributes["request"] != "contact [REDACTED]" || !log.Redacted {
		t.Error("sensitive text in a regular attribute value was not redacted")
	}
}

func TestRedactPreservesExistingRedactedFlag(t *testing.T) {
	log := Redact(schema.Log{Message: "clean", Redacted: true})
	if !log.Redacted {
		t.Error("Redact cleared an existing redacted flag")
	}
}
