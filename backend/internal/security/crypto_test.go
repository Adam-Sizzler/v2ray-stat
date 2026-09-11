package security

import (
	"testing"
	"time"
)

func TestSignAndVerifyJWT(t *testing.T) {
	secret := "test-secret-key-12345"

	claims := SessionClaims{
		SessionID:         "sess-abc",
		SubpageConfigUUID: "cfg-xyz",
		Exp:               time.Now().Add(10 * time.Minute).Unix(),
	}

	token, err := SignJWT(claims, secret)
	if err != nil {
		t.Fatalf("SignJWT failed: %v", err)
	}

	verified, err := VerifySessionJWT(token, secret)
	if err != nil {
		t.Fatalf("VerifySessionJWT failed: %v", err)
	}

	if verified.SessionID != claims.SessionID {
		t.Errorf("got session ID %q, want %q", verified.SessionID, claims.SessionID)
	}
	if verified.SubpageConfigUUID != claims.SubpageConfigUUID {
		t.Errorf("got subpage config %q, want %q", verified.SubpageConfigUUID, claims.SubpageConfigUUID)
	}

	// Verify with generic claims
	generic, err := VerifyJWT(token, secret)
	if err != nil {
		t.Fatalf("VerifyJWT failed: %v", err)
	}
	if generic["sessionId"] != claims.SessionID {
		t.Errorf("got %v, want %s", generic["sessionId"], claims.SessionID)
	}

	// Test expired token
	expiredClaims := SessionClaims{
		SessionID: "sess-expired",
		Exp:       time.Now().Add(-10 * time.Minute).Unix(),
	}
	expiredToken, err := SignJWT(expiredClaims, secret)
	if err != nil {
		t.Fatalf("SignJWT expired failed: %v", err)
	}
	if _, err := VerifySessionJWT(expiredToken, secret); err == nil {
		t.Error("expected error for expired token, got nil")
	}

	// Test invalid signature
	if _, err := VerifySessionJWT(token, "wrong-secret"); err == nil {
		t.Error("expected error for invalid signature, got nil")
	}
}

func TestEncryptDecryptUUID(t *testing.T) {
	secret := "master-secret-key"
	uuid := "550e8400-e29b-41d4-a716-446655440000"

	encrypted, err := EncryptUUID(uuid, secret)
	if err != nil {
		t.Fatalf("EncryptUUID failed: %v", err)
	}

	decrypted, err := DecryptUUID(encrypted, secret)
	if err != nil {
		t.Fatalf("DecryptUUID failed: %v", err)
	}

	if decrypted != uuid {
		t.Errorf("decrypted %q, want %q", decrypted, uuid)
	}
}
