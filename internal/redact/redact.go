package redact

import (
	"regexp"
	"strings"
	"unicode"

	"centilog/internal/schema"
)

const Mask = "[REDACTED]"

const secretKeyPattern = `(password|passwd|pwd|secret[_-]?key|secret|client[_-]?secret|api[[:space:]_-]?(key|token)|access[_-]?key|(access|refresh|id)[_-]?token|token|authorization|auth|cookie|credential|credentials)`

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)(["']?` + secretKeyPattern + `["']?\s*[:=]\s*)("(\\.|[^"\\])*"|'(\\.|[^'\\])*'|[^\r\n,;&}]+)`)
	bearerTokenPattern      = regexp.MustCompile(`(?i)(\bBearer\s+)[A-Za-z0-9._~+/-]+=*`)
	awsAccessKeyPattern     = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)
	emailPattern            = regexp.MustCompile(`[A-Za-z0-9.!#$%&'*+/=?^_{}|~-]+@[A-Za-z0-9-]+([.][A-Za-z0-9-]+)+`)
	cardNumberPattern       = regexp.MustCompile(`[0-9]([ -]?[0-9]){12,18}`)
)

// Redact masks sensitive content in a log message and sensitive attribute values.
func Redact(log schema.Log) schema.Log {
	changed := false
	message := redactMessage(log.Message)
	if message != log.Message {
		log.Message = message
		changed = true
	}

	attributes := make(map[string]string, len(log.Attributes))
	for key, value := range log.Attributes {
		redactedValue := value
		if sensitiveAttributeKey(key) {
			if value != "" && value != Mask {
				redactedValue = Mask
			}
		} else {
			redactedValue = redactMessage(value)
		}
		if redactedValue != value {
			changed = true
		}
		attributes[key] = redactedValue
	}
	log.Attributes = attributes
	log.Redacted = log.Redacted || changed
	return log
}

func redactMessage(message string) string {
	message = secretAssignmentPattern.ReplaceAllStringFunc(message, maskAssignment)
	message = bearerTokenPattern.ReplaceAllString(message, "${1}"+Mask)
	message = awsAccessKeyPattern.ReplaceAllString(message, Mask)
	message = emailPattern.ReplaceAllString(message, Mask)
	return redactCardNumbers(message)
}

func maskAssignment(match string) string {
	parts := secretAssignmentPattern.FindStringSubmatch(match)
	if len(parts) < 2 {
		return match
	}
	prefix := parts[1]
	value := strings.TrimSpace(match[len(prefix):])
	if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
		return prefix + string(value[0]) + Mask + string(value[len(value)-1])
	}
	return prefix + Mask
}

func redactCardNumbers(message string) string {
	matches := cardNumberPattern.FindAllStringIndex(message, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		start, end := matches[i][0], matches[i][1]
		if (start > 0 && isASCIIDigit(message[start-1])) || (end < len(message) && isASCIIDigit(message[end])) {
			continue
		}
		if !passesLuhn(message[start:end]) {
			continue
		}
		message = message[:start] + Mask + message[end:]
	}
	return message
}

func passesLuhn(candidate string) bool {
	digits := make([]int, 0, len(candidate))
	for i := 0; i < len(candidate); i++ {
		char := candidate[i]
		switch {
		case char >= '0' && char <= '9':
			digits = append(digits, int(char-'0'))
		case char == ' ' || char == '-':
		default:
			return false
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}

	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		digit := digits[i]
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}

func isASCIIDigit(char byte) bool {
	return char >= '0' && char <= '9'
}

func sensitiveAttributeKey(key string) bool {
	var parts []string
	for _, part := range strings.FieldsFunc(key, func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsDigit(char)
	}) {
		parts = append(parts, strings.ToLower(part))
	}
	flat := strings.Join(parts, "")
	for _, part := range parts {
		switch part {
		case "password", "passwd", "pwd", "token", "secret", "authorization", "auth", "cookie", "credential", "credentials", "jwt", "session", "bearer":
			return true
		}
	}
	for _, marker := range []string{
		"apikey", "accesskey", "privatekey", "secretkey", "signingkey", "clientsecret",
		"accesstoken", "refreshtoken", "idtoken", "authtoken", "sessiontoken", "sessionid",
	} {
		if strings.Contains(flat, marker) {
			return true
		}
	}
	return false
}
