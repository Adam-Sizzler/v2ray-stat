package users

import "testing"

func TestBuildInboundUsersUsesSingboxProtocolCredentials(t *testing.T) {
	users := []inboundUserCredentials{
		{
			Username:       "alice",
			VLESSUUID:      "9f76f8d8-daf1-4db6-b045-0987cd5e09a2",
			TrojanPassword: "trojan-secret",
			Hysteria2Pass:  "hy-secret",
		},
	}

	tests := []struct {
		name      string
		protocol  string
		wantKey   string
		wantValue any
	}{
		{name: "vmess uses uuid", protocol: "vmess", wantKey: "uuid", wantValue: users[0].VLESSUUID},
		{name: "hysteria uses auth_str", protocol: "hysteria", wantKey: "auth_str", wantValue: "hy-secret"},
		{name: "hysteria2 uses password", protocol: "hysteria2", wantKey: "password", wantValue: "hy-secret"},
		{name: "tuic uses password", protocol: "tuic", wantKey: "password", wantValue: "trojan-secret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items := buildInboundUsers(tc.protocol, users)
			if len(items) != 1 {
				t.Fatalf("got %d users, want 1", len(items))
			}
			item, ok := items[0].(map[string]any)
			if !ok {
				t.Fatalf("unexpected item type %T", items[0])
			}
			if got := item[tc.wantKey]; got != tc.wantValue {
				t.Fatalf("field %s got %#v, want %#v", tc.wantKey, got, tc.wantValue)
			}
		})
	}

	tuic := buildInboundUsers("tuic", users)[0].(map[string]any)
	if got := tuic["uuid"]; got != users[0].VLESSUUID {
		t.Fatalf("tuic uuid got %#v, want %#v", got, users[0].VLESSUUID)
	}
}

func TestParseDeployCoreState(t *testing.T) {
	tests := []struct {
		name        string
		message     string
		wantHas     bool
		wantReady   bool
		wantMessage string
	}{
		{
			name:        "ready core marks node connected",
			message:     "success: users=4 core_ready=true core_process_after=running",
			wantHas:     true,
			wantReady:   true,
			wantMessage: "",
		},
		{
			name:        "failed core reports reload error",
			message:     `success: users=4 core_ready=false reload_error="parse config: unknown outbound"`,
			wantHas:     true,
			wantReady:   false,
			wantMessage: "parse config: unknown outbound",
		},
		{
			name:        "missing core readiness does not imply connected",
			message:     "success: users=4 restarted=true",
			wantHas:     false,
			wantReady:   false,
			wantMessage: "",
		},
		{
			name:        "invalid core readiness is a failed core state",
			message:     "success: core_ready=maybe",
			wantHas:     true,
			wantReady:   false,
			wantMessage: "success: core_ready=maybe",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotHas, gotReady, gotMessage := parseDeployCoreState(tc.message)
			if gotHas != tc.wantHas {
				t.Fatalf("has got %v, want %v", gotHas, tc.wantHas)
			}
			if gotReady != tc.wantReady {
				t.Fatalf("ready got %v, want %v", gotReady, tc.wantReady)
			}
			if gotMessage != tc.wantMessage {
				t.Fatalf("message got %q, want %q", gotMessage, tc.wantMessage)
			}
		})
	}
}

func TestOptionalStatusMessage(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		want      string
		wantDBNil bool
	}{
		{
			name:      "empty message becomes null",
			message:   "",
			want:      "",
			wantDBNil: true,
		},
		{
			name:      "whitespace message becomes null",
			message:   "  ",
			want:      "",
			wantDBNil: true,
		},
		{
			name:      "error message is preserved",
			message:   " Core error: parse config failed ",
			want:      "Core error: parse config failed",
			wantDBNil: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, gotDB := optionalStatusMessage(tc.message)
			if got != tc.want {
				t.Fatalf("message got %q, want %q", got, tc.want)
			}
			if (gotDB == nil) != tc.wantDBNil {
				t.Fatalf("db nil got %v, want %v", gotDB == nil, tc.wantDBNil)
			}
		})
	}
}

func TestHashedSet(t *testing.T) {
	hs := NewHashedSet()
	hs.Add("user1")
	hs.Add("user2")
	hs.Add("user1") // duplicate
	if hs.Size() != 2 {
		t.Fatalf("expected size 2, got %d", hs.Size())
	}
	hash := hs.Hash64String()
	if len(hash) != 16 {
		t.Fatalf("expected 16-char hex hash, got %q", hash)
	}
}

func TestDeleteField(t *testing.T) {
	m := map[string]any{
		"tag":   "vless-in",
		"users": []any{"alice", "bob"},
	}
	cleaned := deleteField(m, "users").(map[string]any)
	if _, ok := cleaned["users"]; ok {
		t.Fatalf("expected users to be deleted")
	}
	if cleaned["tag"] != "vless-in" {
		t.Fatalf("expected tag to be preserved, got %v", cleaned["tag"])
	}
}
