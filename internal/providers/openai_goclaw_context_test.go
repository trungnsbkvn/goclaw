package providers

import "testing"

func chatCtxOptions() map[string]any {
	return map[string]any{
		OptSessionKey: "agent:sales:zalo_personal:direct:12345",
		OptChannel:    "zalo_personal",
		OptChatID:     "12345",
		OptPeerKind:   "direct",
		OptUserID:     "u-1",
	}
}

func TestContextMetadataEmittedForOpenAICompatWhenEnabled(t *testing.T) {
	p := NewOpenAIProvider("jushub-brain", "k", "http://127.0.0.1:8090/api/v1/agent", "m")
	p.WithProviderType("openai_compat")
	p.WithContextPassthrough(true)

	md := p.buildContextMetadata(ChatRequest{Options: chatCtxOptions()})
	if md == nil {
		t.Fatal("expected metadata for an opted-in openai_compat provider")
	}
	for k, want := range map[string]string{
		"channel":     "zalo_personal",
		"chat_id":     "12345",
		"peer_kind":   "direct",
		"user_id":     "u-1",
		"session_key": "agent:sales:zalo_personal:direct:12345",
	} {
		if got, _ := md[k].(string); got != want {
			t.Errorf("metadata[%q] = %q, want %q", k, got, want)
		}
	}
}

// The leak guard. Chat metadata is operational data about who is talking to
// whom; it must never reach a third-party host just because that host speaks the
// OpenAI protocol. Even with the flag wrongly set, a named vendor gets nothing.
func TestContextMetadataNeverLeaksToOtherProviderTypes(t *testing.T) {
	for _, pt := range []string{
		"openai", "groq", "deepseek", "openrouter", "mistral", "xai",
		"dashscope", "ollama", "gemini_native", "anthropic_native", "",
	} {
		t.Run(pt, func(t *testing.T) {
			p := NewOpenAIProvider("vendor", "k", "https://api.vendor.com/v1", "m")
			p.WithProviderType(pt)
			p.WithContextPassthrough(true) // deliberately mis-set

			if md := p.buildContextMetadata(ChatRequest{Options: chatCtxOptions()}); md != nil {
				t.Errorf("providerType %q leaked chat metadata: %v", pt, md)
			}
		})
	}
}

func TestContextMetadataOffByDefault(t *testing.T) {
	p := NewOpenAIProvider("jushub-brain", "k", "http://127.0.0.1:8090/api/v1/agent", "m")
	p.WithProviderType("openai_compat")
	// No WithContextPassthrough call at all.

	if md := p.buildContextMetadata(ChatRequest{Options: chatCtxOptions()}); md != nil {
		t.Errorf("passthrough must be opt-in; got %v", md)
	}
}

// Utility calls (title generation, intent classification, history compaction)
// build their own ChatRequest with no chat options. Emitting a half-populated
// metadata object would make a self-hosted backend unable to tell them apart
// from a real conversation turn — so we send nothing.
func TestContextMetadataNilWithoutChatID(t *testing.T) {
	p := NewOpenAIProvider("jushub-brain", "k", "http://127.0.0.1:8090/api/v1/agent", "m")
	p.WithProviderType("openai_compat")
	p.WithContextPassthrough(true)

	cases := map[string]map[string]any{
		"nil options":    nil,
		"empty options":  {},
		"no chat_id":     {OptChannel: "zalo_personal", OptUserID: "u-1"},
		"blank chat_id":  {OptChatID: "", OptChannel: "zalo_personal"},
		"non-string ids": {OptChatID: 12345, OptChannel: "zalo_personal"},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if md := p.buildContextMetadata(ChatRequest{Options: opts}); md != nil {
				t.Errorf("expected nil metadata for a utility call, got %v", md)
			}
		})
	}
}

func TestParseContextPassthroughDefaultsFalse(t *testing.T) {
	// Guards the store-side parser through the provider's own setter path.
	p := NewOpenAIProvider("x", "k", "http://h/v1", "m")
	p.WithProviderType("openai_compat")
	p.WithContextPassthrough(false)
	if md := p.buildContextMetadata(ChatRequest{Options: chatCtxOptions()}); md != nil {
		t.Error("explicit false must disable passthrough")
	}
}
