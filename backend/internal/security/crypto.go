package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Claims map[string]any

const jwtHeaderHS256 = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" // base64.RawURLEncoding of {"alg":"HS256","typ":"JWT"}

type SessionClaims struct {
	SessionID         string `json:"sessionId"`
	SubpageConfigUUID string `json:"subpageConfigUuid"`
	Exp               int64  `json:"exp"`
}

func EncryptUUID(uuidValue, secretKey string) (string, error) {
	key := sha256.Sum256([]byte(secretKey))

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(uuidValue), nil)
	tagSize := gcm.Overhead()
	tagStart := len(ciphertext) - tagSize

	combined := make([]byte, 0, len(nonce)+len(ciphertext))
	combined = append(combined, nonce...)
	combined = append(combined, ciphertext[tagStart:]...)
	combined = append(combined, ciphertext[:tagStart]...)

	return base64.RawURLEncoding.EncodeToString(combined), nil
}

func DecryptUUID(data, secretKey string) (string, error) {
	key := sha256.Sum256([]byte(secretKey))

	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	tagSize := gcm.Overhead()
	if len(raw) < nonceSize+tagSize {
		return "", fmt.Errorf("encrypted payload is too short")
	}

	nonce := raw[:nonceSize]
	tag := raw[nonceSize : nonceSize+tagSize]
	ciphertext := raw[nonceSize+tagSize:]
	sealed := make([]byte, 0, len(ciphertext)+len(tag))
	sealed = append(sealed, ciphertext...)
	sealed = append(sealed, tag...)

	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

func SignJWT(claims any, secret string) (string, error) {
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	payloadPart := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := jwtHeaderHS256 + "." + payloadPart

	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + signature, nil
}

func verifyJWTSignature(token, secret string) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid jwt format")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	expected := mac.Sum(nil)

	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}

	if !hmac.Equal(actual, expected) {
		return nil, fmt.Errorf("invalid jwt signature")
	}

	return base64.RawURLEncoding.DecodeString(parts[1])
}

func VerifySessionJWT(token, secret string) (*SessionClaims, error) {
	payloadBytes, err := verifyJWTSignature(token, secret)
	if err != nil {
		return nil, err
	}

	var claims SessionClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, err
	}

	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return nil, fmt.Errorf("jwt expired")
	}

	return &claims, nil
}

func VerifyJWT(token, secret string) (Claims, error) {
	payloadBytes, err := verifyJWTSignature(token, secret)
	if err != nil {
		return nil, err
	}

	var claims Claims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, err
	}

	if expValue, ok := claims["exp"]; ok {
		expiration, err := NumericClaimToInt64(expValue)
		if err != nil {
			return nil, err
		}

		if time.Now().Unix() > expiration {
			return nil, fmt.Errorf("jwt expired")
		}
	}

	return claims, nil
}

func NumericClaimToInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return 0, fmt.Errorf("invalid numeric claim")
		}
		return int64(typed), nil
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case json.Number:
		return typed.Int64()
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported numeric claim type %T", value)
	}
}

func RandomToken(length int) string {
	if length <= 0 {
		return ""
	}

	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	token := base64.RawURLEncoding.EncodeToString(raw)
	if len(token) > length {
		return token[:length]
	}

	return token
}
