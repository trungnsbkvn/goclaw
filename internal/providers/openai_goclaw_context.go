package providers

// Thread-context passthrough for self-hosted `openai_compat` backends.
//
// Why this exists: an operator can point an `openai_compat` provider at their
// OWN service and use goclaw purely as a chat transport, letting that service
// run the agent loop (its own persona, tools, history, metering). For that to
// work the backend has to know WHICH conversation a request belongs to — a
// Chat Completions body alone carries no chat id, no channel, no peer kind.
//
// goclaw already computes all of it: loop_pipeline_callbacks.go puts
// OptSessionKey / OptChannel / OptChatID / OptPeerKind / OptUserID / OptAgentID
// into ChatRequest.Options on every main-loop call. They were simply never
// serialised. This file emits them as the OpenAI-standard `metadata` object.
//
// Two deliberate constraints:
//
//   - OPT-IN per provider row (`settings.context_passthrough`). Chat metadata is
//     operational data about who is talking to whom; it must never be shipped to
//     a third-party host just because that host speaks the OpenAI protocol.
//   - `openai_compat` ONLY. Named vendors (openai, groq, deepseek, …) never
//     receive it even if the flag is somehow set, so a mis-set flag on a vendor
//     row cannot leak.
//
// Utility calls (title generation, intent classification, history compaction)
// build their own ChatRequest without these options, so they carry no metadata —
// which is precisely how a self-hosted backend tells housekeeping apart from a
// real conversation turn.

// OptAgentID passes the agent key for MCP bridge context.
// (Defined alongside the other Opt* keys in claude_cli.go; re-declared here only
// if absent there.)

// metadataKeys maps the wire field name to the ChatRequest option key.
var metadataKeys = map[string]string{
	"session_key": OptSessionKey,
	"channel":     OptChannel,
	"chat_id":     OptChatID,
	"peer_kind":   OptPeerKind,
	"user_id":     OptUserID,
}

// buildContextMetadata returns the `metadata` object for a request, or nil when
// passthrough is off, the provider is not `openai_compat`, or no chat context is
// present (a utility call).
func (p *OpenAIProvider) buildContextMetadata(req ChatRequest) map[string]any {
	if !p.contextPassthrough {
		return nil
	}
	// Hard gate: self-hosted compat endpoints only.
	if p.providerType != "openai_compat" {
		return nil
	}
	if req.Options == nil {
		return nil
	}

	md := make(map[string]any, len(metadataKeys)+1)
	for wire, opt := range metadataKeys {
		if v := extractStringOpt(req.Options, opt); v != "" {
			md[wire] = v
		}
	}
	// A request without a chat id is housekeeping, not a conversation turn —
	// send nothing rather than a half-populated object the backend would have to
	// second-guess.
	if _, ok := md["chat_id"]; !ok {
		return nil
	}
	if p.name != "" {
		md["provider"] = p.name
	}
	return md
}

// WithContextPassthrough enables emitting the `metadata` object described above.
// Wired from the provider row's `settings.context_passthrough`.
func (p *OpenAIProvider) WithContextPassthrough(on bool) *OpenAIProvider {
	p.contextPassthrough = on
	return p
}
