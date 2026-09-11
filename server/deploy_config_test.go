package server

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"exodus-node/config"

	"github.com/iancoleman/orderedmap"
)

func TestBuildSingboxConfigWithV2RayAPIPreservesTopLevelOrder(t *testing.T) {
	raw := []byte(`{
  "log": {"level":"debug","output":"access.log","timestamp":true},
  "dns": {"servers":[{"type":"local","tag":"dns-main"}],"final":"dns-main"},
  "inbounds": [{"tag":"trojan-in","type":"trojan","listen":"0.0.0.0","listen_port":10443,"users":[{"name":"u1","password":"p1"}]}],
  "outbounds": [{"tag":"direct","type":"direct"}],
  "route": {"rules":[{"action":"sniff"}]}
}`)

	out, _, err := BuildSingboxConfigWithV2RayAPI(raw, BuildOptions{
		Listen:  "127.0.0.1:10085",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("BuildSingboxConfigWithV2RayAPI failed: %v", err)
	}

	var cfg orderedmap.OrderedMap
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal built config: %v", err)
	}

	wantTop := []string{"log", "dns", "inbounds", "outbounds", "route", "experimental"}
	if !reflect.DeepEqual(cfg.Keys(), wantTop) {
		t.Fatalf("top-level keys mismatch, want=%v got=%v", wantTop, cfg.Keys())
	}

	experimentalRaw, ok := cfg.Get("experimental")
	if !ok {
		t.Fatalf("missing experimental section")
	}
	experimental := mustOrderedMap(t, experimentalRaw)
	wantExperimental := []string{"cache_file", "v2ray_api"}
	if !reflect.DeepEqual(experimental.Keys(), wantExperimental) {
		t.Fatalf("experimental keys mismatch, want=%v got=%v", wantExperimental, experimental.Keys())
	}
}

func TestBuildSingboxConfigWithV2RayAPIPreservesNestedObjectOrder(t *testing.T) {
	raw := []byte(`{
  "log": {"level":"debug","output":"access.log","timestamp":true},
  "dns": {
    "servers": [{"type":"local","tag":"dns-main"}],
    "rules": [{"rule_set":["category-ads-all"],"action":"predefined","rcode":"NOERROR"}],
    "final": "dns-main"
  },
  "inbounds": [{"tag":"trojan-in","type":"trojan","listen":"0.0.0.0","listen_port":10443,"users":[]}],
  "outbounds": [{"tag":"direct","type":"direct"}],
  "route": {"rules":[{"action":"sniff"}]}
}`)

	out, _, err := BuildSingboxConfigWithV2RayAPI(raw, BuildOptions{
		Listen:  "127.0.0.1:10085",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("BuildSingboxConfigWithV2RayAPI failed: %v", err)
	}

	var cfg orderedmap.OrderedMap
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal built config: %v", err)
	}

	dns := mustOrderedMap(t, mustGet(t, cfg, "dns"))
	dnsRules := mustArray(t, mustGet(t, dns, "rules"))
	firstRule := mustOrderedMap(t, dnsRules[0])
	wantRuleOrder := []string{"rule_set", "action", "rcode"}
	if !reflect.DeepEqual(firstRule.Keys(), wantRuleOrder) {
		t.Fatalf("dns.rules[0] keys mismatch, want=%v got=%v", wantRuleOrder, firstRule.Keys())
	}
}

func TestShouldReloadCoreHonorsForceRestart(t *testing.T) {
	restart := true
	noRestart := false
	force := true
	soft := false

	cases := []struct {
		name          string
		task          DeployConfigTaskPayload
		configChanged bool
		want          bool
	}{
		{
			name:          "soft restart changed config",
			task:          DeployConfigTaskPayload{Restart: &restart, ForceRestart: &soft},
			configChanged: true,
			want:          true,
		},
		{
			name:          "soft restart unchanged config",
			task:          DeployConfigTaskPayload{Restart: &restart, ForceRestart: &soft},
			configChanged: false,
			want:          false,
		},
		{
			name:          "force restart unchanged config",
			task:          DeployConfigTaskPayload{Restart: &restart, ForceRestart: &force},
			configChanged: false,
			want:          true,
		},
		{
			name:          "camel force restart unchanged config",
			task:          DeployConfigTaskPayload{Restart: &restart, ForceRestartCamel: &force},
			configChanged: false,
			want:          true,
		},
		{
			name:          "restart disabled",
			task:          DeployConfigTaskPayload{Restart: &noRestart},
			configChanged: true,
			want:          false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldReloadCore(tc.task, tc.configChanged); got != tc.want {
				t.Fatalf("unexpected reload decision: got=%v want=%v", got, tc.want)
			}
		})
	}
}

// Regression test: DeployConfig used to pre-default task.Listen to the fixed
// port before ever calling BuildSingboxConfigWithV2RayAPI, which made
// opt.Listen always non-empty and permanently shadowed the profile config's
// own experimental.v2ray_api.listen. This asserts the underlying priority
// chain Build is supposed to honor: when no explicit BuildOptions.Listen is
// given, the profile config's own listen value must win over the fixed
// default.
func TestBuildSingboxConfigWithV2RayAPIFallsBackToProfileListenWhenNoExplicitOverride(t *testing.T) {
	raw := []byte(`{
  "log": {"level":"debug"},
  "inbounds": [{"tag":"trojan-in","type":"trojan","users":[{"name":"u1"}]}],
  "outbounds": [{"tag":"direct","type":"direct"}],
  "experimental": {
    "v2ray_api": {
      "listen": "127.0.0.1:10086",
      "stats": {
        "enabled": true,
        "inbounds": ["trojan-in"],
        "outbounds": ["direct"],
        "users": ["u1"]
      }
    }
  }
}`)

	out, summary, err := BuildSingboxConfigWithV2RayAPI(raw, BuildOptions{
		Listen:  "",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("BuildSingboxConfigWithV2RayAPI failed: %v", err)
	}

	if summary.Listen != "127.0.0.1:10086" {
		t.Fatalf("expected summary.Listen from profile config '127.0.0.1:10086', got %v", summary.Listen)
	}

	var cfg orderedmap.OrderedMap
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal built config: %v", err)
	}
	experimental := mustOrderedMap(t, mustGet(t, cfg, "experimental"))
	v2rayAPI := mustOrderedMap(t, mustGet(t, experimental, "v2ray_api"))
	if val, ok := v2rayAPI.Get("listen"); !ok || val != "127.0.0.1:10086" {
		t.Fatalf("expected v2ray_api.listen='127.0.0.1:10086' from profile config, got %v", val)
	}
}

func mustGet(t *testing.T, o orderedmap.OrderedMap, key string) any {
	t.Helper()
	v, ok := o.Get(key)
	if !ok {
		t.Fatalf("missing key %q", key)
	}
	return v
}

func mustOrderedMap(t *testing.T, v any) orderedmap.OrderedMap {
	t.Helper()
	m, ok := v.(orderedmap.OrderedMap)
	if !ok {
		t.Fatalf("expected ordered map, got %T", v)
	}
	return m
}

func mustArray(t *testing.T, v any) []any {
	t.Helper()
	arr, ok := v.([]any)
	if !ok {
		t.Fatalf("expected array, got %T", v)
	}
	return arr
}

func TestBuildSingboxConfigWithV2RayAPIOptionOverrides(t *testing.T) {
	raw := []byte(`{
  "log": {"level":"debug"},
  "inbounds": [{"tag":"trojan-in","type":"trojan","users":[{"name":"u1"}]}],
  "outbounds": [{"tag":"direct","type":"direct"}],
  "experimental": {
    "cache_file": {
      "enabled": false,
      "path": "custom.db",
      "store_fakeip": true
    },
    "v2ray_api": {
      "listen": "127.0.0.1:10086",
      "stats": {
        "enabled": true,
        "inbounds": ["trojan-in"],
        "outbounds": ["direct"],
        "users": ["u1"]
      }
    }
  }
}`)

	out, summary, err := BuildSingboxConfigWithV2RayAPI(raw, BuildOptions{
		Listen:  "127.0.0.1:10085",
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("BuildSingboxConfigWithV2RayAPI failed: %v", err)
	}

	var cfg orderedmap.OrderedMap
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unmarshal built config: %v", err)
	}

	experimental := mustOrderedMap(t, mustGet(t, cfg, "experimental"))
	cacheFile := mustOrderedMap(t, mustGet(t, experimental, "cache_file"))

	// Should preserve the explicitly defined cache_file options, including enabled=false
	if val, ok := cacheFile.Get("enabled"); !ok || val != false {
		t.Fatalf("expected cache_file.enabled=false, got %v", val)
	}
	if val, ok := cacheFile.Get("path"); !ok || val != "custom.db" {
		t.Fatalf("expected cache_file.path='custom.db', got %v", val)
	}

	v2rayAPI := mustOrderedMap(t, mustGet(t, experimental, "v2ray_api"))
	// Should use listen address from BuildOptions
	if val, ok := v2rayAPI.Get("listen"); !ok || val != "127.0.0.1:10085" {
		t.Fatalf("expected v2ray_api.listen='127.0.0.1:10085', got %v", val)
	}

	stats := mustOrderedMap(t, mustGet(t, v2rayAPI, "stats"))
	// Should preserve stats options because they were not empty
	if val, ok := stats.Get("enabled"); !ok || val != true {
		t.Fatalf("expected stats.enabled=true, got %v", val)
	}

	// Assert summary also has the overridden options
	if !reflect.DeepEqual(summary.Inbounds, []string{"trojan-in"}) {
		t.Fatalf("unexpected summary.Inbounds: %v", summary.Inbounds)
	}
	if !reflect.DeepEqual(summary.Outbounds, []string{"direct"}) {
		t.Fatalf("unexpected summary.Outbounds: %v", summary.Outbounds)
	}
	if !reflect.DeepEqual(summary.Users, []string{"u1"}) {
		t.Fatalf("unexpected summary.Users: %v", summary.Users)
	}
}

func TestLogInternalUserExtractionWithRemoteHashes(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	logger := config.NewExodusLogger(&buf, "debug")

	rawConfig := json.RawMessage(`{
		"inbounds": [
			{
				"tag": "vless-in",
				"type": "vless",
				"users": [
					{"uuid": "11111111-2222-3333-4444-555555555555"}
				]
			}
		]
	}`)

	hashes := &DeployHashesPayload{
		EmptyConfig: "remote-empty-config-sha256",
		Inbounds: []DeployInboundHash{
			{Tag: "vless-in", Hash: "remote-hash-123", UsersCount: 1},
		},
	}

	logInternalUserExtraction(logger, rawConfig, hashes)

	out := buf.String()
	if !strings.Contains(out, "▸ Empty Config Hash: remote-empty-config-sha256") {
		t.Fatalf("expected remote empty config hash, got: %q", out)
	}
	if !strings.Contains(out, "(remote-hash-123)") {
		t.Fatalf("expected remote inbound hash in log, got: %q", out)
	}

	// Test fallback when hashes is nil
	buf.Reset()
	logInternalUserExtraction(logger, rawConfig, nil)
	outFallback := buf.String()
	if !strings.Contains(outFallback, "(N/A)") {
		t.Fatalf("expected (N/A) fallback when remote hashes missing, got: %q", outFallback)
	}
}
