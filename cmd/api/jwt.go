package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

const jwtLifetime = 12 * time.Hour

type tokenClaims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	Issued  int64  `json:"iat"`
	Expires int64  `json:"exp"`
}

type jwtManager struct {
	secret []byte
	now    func() time.Time
}

func newJWTManager(secret []byte) *jwtManager {
	return &jwtManager{secret: append([]byte(nil), secret...), now: time.Now}
}

func (manager *jwtManager) Sign(account user) (string, time.Time, error) {
	now := manager.now().UTC()
	expires := now.Add(jwtLifetime)
	claims := tokenClaims{
		Subject: strconv.FormatInt(account.ID, 10),
		Email:   account.Email,
		Role:    account.Role,
		Issued:  now.Unix(),
		Expires: expires.Unix(),
	}
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", time.Time{}, errors.New("encode token header")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, errors.New("encode token claims")
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := manager.sign(unsigned)
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), expires, nil
}

func (manager *jwtManager) Parse(token string) (tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(manager.secret) == 0 {
		return tokenClaims{}, errors.New("invalid token")
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(providedSignature, manager.sign(parts[0]+"."+parts[1])) {
		return tokenClaims{}, errors.New("invalid token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return tokenClaims{}, errors.New("invalid token")
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if json.Unmarshal(headerBytes, &header) != nil || header.Algorithm != "HS256" || header.Type != "JWT" {
		return tokenClaims{}, errors.New("invalid token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return tokenClaims{}, errors.New("invalid token")
	}
	var claims tokenClaims
	if json.Unmarshal(payload, &claims) != nil || claims.Subject == "" || claims.Email == "" || (claims.Role != "admin" && claims.Role != "viewer") || claims.Expires <= manager.now().Unix() {
		return tokenClaims{}, errors.New("invalid token")
	}
	if _, err := strconv.ParseInt(claims.Subject, 10, 64); err != nil {
		return tokenClaims{}, errors.New("invalid token")
	}
	return claims, nil
}

func (manager *jwtManager) sign(value string) []byte {
	hash := hmac.New(sha256.New, manager.secret)
	_, _ = hash.Write([]byte(value))
	return hash.Sum(nil)
}
