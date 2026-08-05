//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestResolveGroupTitlesUsesChannelContacts(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	tenantID := uuid.New()
	if _, err := db.Exec(`INSERT INTO tenants (id, name, slug, status) VALUES (?, 'T', 't-pending-title', 'active')`, tenantID.String()); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	ctx := store.WithTenantID(context.Background(), tenantID)
	contacts := NewSQLiteContactStore(db)
	if err := contacts.UpsertContact(ctx, "discord", "discord-main", "thread-1", "", "launch-thread", "", "group", "group", "", ""); err != nil {
		t.Fatalf("upsert thread contact: %v", err)
	}
	if err := contacts.UpsertContact(ctx, "discord", "discord-main", "parent-1", "", "product-planning", "", "group", "group", "", ""); err != nil {
		t.Fatalf("upsert parent contact: %v", err)
	}

	pending := NewSQLitePendingMessageStore(db)
	titles, err := pending.ResolveGroupTitles(ctx, []store.PendingMessageGroup{
		{ChannelName: "discord-main", HistoryKey: "thread-1"},
		{ChannelName: "discord-main", HistoryKey: "parent-1"},
	})
	if err != nil {
		t.Fatalf("ResolveGroupTitles: %v", err)
	}
	if titles["discord-main:thread-1"] != "launch-thread" {
		t.Fatalf("thread title = %q, want launch-thread", titles["discord-main:thread-1"])
	}
	if titles["discord-main:parent-1"] != "product-planning" {
		t.Fatalf("parent title = %q, want product-planning", titles["discord-main:parent-1"])
	}
}

// ListGroups selects MAX(created_at) AS last_activity. The aggregate erases the
// column's declared type, so the driver hands back a TEXT value and scanning it
// straight into a time.Time fails on every row. That made ListGroups return an
// error unconditionally, and history compaction — its only caller — had never
// run once in production: it logged compaction.sweep_failed every 10 minutes.
// Guard the scan, and assert last_activity actually round-trips.
func TestListGroupsScansAggregatedLastActivity(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	tenantID := uuid.New()
	if _, err := db.Exec(`INSERT INTO tenants (id, name, slug, status) VALUES (?, 'T', 't-pending-groups', 'active')`, tenantID.String()); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	ctx := store.WithTenantID(context.Background(), tenantID)
	pending := NewSQLitePendingMessageStore(db)

	if err := pending.AppendBatch(ctx, []store.PendingMessage{
		{ChannelName: "zalo-pws", HistoryKey: "user:1", Sender: "a", Body: "first"},
		{ChannelName: "zalo-pws", HistoryKey: "user:1", Sender: "b", Body: "second"},
	}); err != nil {
		t.Fatalf("AppendBatch: %v", err)
	}

	groups, err := pending.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	g := groups[0]
	if g.ChannelName != "zalo-pws" || g.HistoryKey != "user:1" {
		t.Fatalf("group = %s/%s, want zalo-pws/user:1", g.ChannelName, g.HistoryKey)
	}
	if g.MessageCount != 2 {
		t.Fatalf("MessageCount = %d, want 2", g.MessageCount)
	}
	if g.LastActivity.IsZero() {
		t.Fatal("LastActivity is zero — the aggregate timestamp did not survive the scan")
	}
}
