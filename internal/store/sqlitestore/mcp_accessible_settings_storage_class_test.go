//go:build sqlite || sqliteonly

package sqlitestore

import (
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// Regression: an MCP server whose settings column carries TEXT storage class
// must stay visible to ListAccessible.
//
// Live incident 2026-07-28 (prod tenant yp). CacheToolDescriptions wrote the
// merged settings JSON as string(merged), so SQLite stored TEXT while every
// Go-written column (args/headers/env) stored BLOB. scanAccessibleRows then
// scanned that column into a bare json.RawMessage — a *named* []byte with no
// sql.Scanner — and database/sql refuses string → named-[]byte. The scan
// errored, the loop's `continue` dropped the row without logging, and
// ListAccessible returned (0 rows, nil error).
//
// Because the poisoning write happens on the server's first successful
// connect, the chat agent lost every MCP tool permanently while
// /v1/mcp/servers (sqlx path, tolerant scan) kept listing the server as
// present and granted. The symptom reads exactly like a missing grant.
//
// The fix has two halves and this test pins both: settings is now bound as
// []byte (BLOB), and the scan tolerates TEXT rows written by older builds.
func TestListAccessible_SettingsTextStorageClass(t *testing.T) {
	db := newHookTestDB(t)
	tenantID, agentID := seedHookTenantAgent(t, db)
	ctx := sqliteTenantCtx(tenantID)
	mcpStore := NewSQLiteMCPServerStore(db, "")

	srv := &store.MCPServerData{
		Name:                   "jushub",
		Transport:              "streamable-http",
		URL:                    "http://127.0.0.1:8090/api/v1/mcp",
		Enabled:                true,
		RequireUserCredentials: true,
		CreatedBy:              "system",
	}
	if err := mcpStore.CreateServer(ctx, srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	if err := mcpStore.GrantToAgent(ctx, &store.MCPAgentGrant{
		ServerID: srv.ID, AgentID: agentID, Enabled: true, GrantedBy: "system",
	}); err != nil {
		t.Fatalf("GrantToAgent: %v", err)
	}

	assertVisible := func(stage string) {
		t.Helper()
		for _, userID := range []string{"", "1907496579"} {
			got, err := mcpStore.ListAccessible(ctx, agentID, userID)
			if err != nil {
				t.Fatalf("%s: ListAccessible(user=%q): %v", stage, userID, err)
			}
			if len(got) != 1 {
				t.Fatalf("%s: ListAccessible(user=%q) returned %d servers, want 1 — "+
					"a scan failure is silently dropping the row", stage, userID, len(got))
			}
			// Schema v55→56 promoted require_user_credentials out of the
			// settings JSON into its own column, but this query kept selecting
			// the old shape — so the flag always came back false and
			// Manager.LoadForAgent never deferred the server to the per-user
			// path. It connected as a SHARED server on the server-level
			// (catalog-only) credential instead: tools listed, every call
			// refused. Same 2026-07-28 incident, second cause.
			if !got[0].Server.RequireUserCredentials {
				t.Fatalf("%s: ListAccessible(user=%q) lost require_user_credentials — "+
					"per-user MCP resolution will never engage", stage, userID)
			}
		}
	}

	assertVisible("fresh row")

	// The real write path: caching tool descriptions must not change the
	// column's storage class out from under the access query.
	if err := mcpStore.CacheToolDescriptions(ctx, srv.ID, map[string]store.CachedToolInfo{
		"ask_assistant": {Description: "Ask the JusHub AI assistant"},
	}); err != nil {
		t.Fatalf("CacheToolDescriptions: %v", err)
	}
	var class string
	if err := db.QueryRow(`SELECT typeof(settings) FROM mcp_servers WHERE id = ?`, srv.ID).Scan(&class); err != nil {
		t.Fatalf("typeof(settings): %v", err)
	}
	if class != "blob" {
		t.Errorf("settings storage class = %q after CacheToolDescriptions, want \"blob\" "+
			"(a string bind poisons every raw scan of this column)", class)
	}
	assertVisible("after CacheToolDescriptions")

	// Rows already poisoned by an older build must keep working after upgrade —
	// there is no migration for them.
	if _, err := db.Exec(
		`UPDATE mcp_servers SET settings = CAST(settings AS TEXT) WHERE id = ?`, srv.ID); err != nil {
		t.Fatalf("force TEXT settings: %v", err)
	}
	if err := db.QueryRow(`SELECT typeof(settings) FROM mcp_servers WHERE id = ?`, srv.ID).Scan(&class); err != nil {
		t.Fatalf("typeof(settings): %v", err)
	}
	if class != "text" {
		t.Fatalf("setup failed: settings storage class = %q, want \"text\"", class)
	}
	assertVisible("legacy TEXT row")

	// And the settings payload itself must survive the tolerant scan intact,
	// since ListToolsForAgent reads tool_cache back out of it.
	got, err := mcpStore.ListAccessible(ctx, agentID, "")
	if err != nil {
		t.Fatalf("ListAccessible: %v", err)
	}
	if len(got[0].Server.Settings) == 0 {
		t.Error("settings came back empty from a TEXT row — tool_cache would be lost")
	}
}
