//go:build sqlite || sqliteonly

package sqlitestore

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// rawJSON scans SQLite JSON columns regardless of storage class. Rows
// seeded by migration SQL bind '{}' as TEXT (the driver returns string),
// while rows inserted from Go bind json.RawMessage as BLOB (the driver
// returns []byte). database/sql refuses string→named-[]byte conversions,
// so a plain json.RawMessage scan field explodes on seeded rows — live
// 2026-07-26: the webhook worker's ListTenants logged
// "unsupported Scan, storing driver.Value type string into type
// *json.RawMessage" every 2s on the prod sidecar, and the boot-time
// system-config seed hit the same error. Use this type for every db-tagged
// JSON field in the sqlite scan structs.
type rawJSON json.RawMessage

func (r *rawJSON) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*r = nil
	case []byte:
		*r = rawJSON(append([]byte(nil), v...))
	case string:
		*r = rawJSON(v)
	default:
		return fmt.Errorf("rawJSON: unsupported scan type %T", src)
	}
	return nil
}

func (r rawJSON) Value() (driver.Value, error) {
	if len(r) == 0 {
		return nil, nil
	}
	return []byte(r), nil
}

// providerRow is a scan struct for llm_providers rows.
// Uses sqliteTime for created_at/updated_at to handle SQLite text timestamps.
type providerRow struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	Name         string     `json:"name" db:"name"`
	DisplayName  string     `json:"display_name" db:"display_name"`
	ProviderType string     `json:"provider_type" db:"provider_type"`
	APIBase      string     `json:"api_base" db:"api_base"`
	APIKey       string     `json:"api_key" db:"api_key"`
	Enabled      bool       `json:"enabled" db:"enabled"`
	Settings     rawJSON    `json:"settings" db:"settings"`
	CreatedAt    sqliteTime `json:"created_at" db:"created_at"`
	UpdatedAt    sqliteTime `json:"updated_at" db:"updated_at"`
	TenantID     uuid.UUID  `json:"tenant_id" db:"tenant_id"`
}

func (r *providerRow) toLLMProviderData() store.LLMProviderData {
	return store.LLMProviderData{
		BaseModel:    store.BaseModel{ID: r.ID, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time},
		TenantID:     r.TenantID,
		Name:         r.Name,
		DisplayName:  r.DisplayName,
		ProviderType: r.ProviderType,
		APIBase:      r.APIBase,
		APIKey:       r.APIKey,
		Enabled:      r.Enabled,
		Settings:     json.RawMessage(r.Settings),
	}
}

// tenantRow is a scan struct for tenants rows.
type tenantRow struct {
	ID        uuid.UUID  `json:"id" db:"id"`
	Name      string     `json:"name" db:"name"`
	Slug      string     `json:"slug" db:"slug"`
	Status    string     `json:"status" db:"status"`
	Settings  rawJSON    `json:"settings" db:"settings"`
	CreatedAt sqliteTime `json:"created_at" db:"created_at"`
	UpdatedAt sqliteTime `json:"updated_at" db:"updated_at"`
}

func (r *tenantRow) toTenantData() store.TenantData {
	return store.TenantData{
		ID:        r.ID,
		Name:      r.Name,
		Slug:      r.Slug,
		Status:    r.Status,
		Settings:  json.RawMessage(r.Settings),
		CreatedAt: r.CreatedAt.Time,
		UpdatedAt: r.UpdatedAt.Time,
	}
}

// tenantUserRow is a scan struct for tenant_users rows.
type tenantUserRow struct {
	ID          uuid.UUID  `json:"id" db:"id"`
	TenantID    uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	UserID      string     `json:"user_id" db:"user_id"`
	DisplayName *string    `json:"display_name" db:"display_name"`
	Role        string     `json:"role" db:"role"`
	Metadata    rawJSON    `json:"metadata" db:"metadata"`
	CreatedAt   sqliteTime `json:"created_at" db:"created_at"`
	UpdatedAt   sqliteTime `json:"updated_at" db:"updated_at"`
}

func (r *tenantUserRow) toTenantUserData() store.TenantUserData {
	return store.TenantUserData{
		ID:          r.ID,
		TenantID:    r.TenantID,
		UserID:      r.UserID,
		DisplayName: r.DisplayName,
		Role:        r.Role,
		Metadata:    json.RawMessage(r.Metadata),
		CreatedAt:   r.CreatedAt.Time,
		UpdatedAt:   r.UpdatedAt.Time,
	}
}

// mcpServerRow is a scan struct for mcp_servers rows.
// Pointer fields handle nullable columns that sqlx maps to empty string otherwise.
type mcpServerRow struct {
	ID                     uuid.UUID  `json:"id" db:"id"`
	Name                   string     `json:"name" db:"name"`
	DisplayName            *string    `json:"display_name" db:"display_name"`
	Transport              string     `json:"transport" db:"transport"`
	Command                *string    `json:"command" db:"command"`
	Args                   rawJSON    `json:"args" db:"args"`
	URL                    *string    `json:"url" db:"url"`
	Headers                rawJSON    `json:"headers" db:"headers"`
	Env                    rawJSON    `json:"env" db:"env"`
	APIKey                 *string    `json:"api_key" db:"api_key"`
	ToolPrefix             *string    `json:"tool_prefix" db:"tool_prefix"`
	TimeoutSec             int        `json:"timeout_sec" db:"timeout_sec"`
	Settings               rawJSON    `json:"settings" db:"settings"`
	Enabled                bool       `json:"enabled" db:"enabled"`
	RequireUserCredentials bool       `json:"require_user_credentials" db:"require_user_credentials"`
	CreatedBy              string     `json:"created_by" db:"created_by"`
	CreatedAt              sqliteTime `json:"created_at" db:"created_at"`
	UpdatedAt              sqliteTime `json:"updated_at" db:"updated_at"`
}

func (r *mcpServerRow) toMCPServerData() store.MCPServerData {
	return store.MCPServerData{
		BaseModel:              store.BaseModel{ID: r.ID, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time},
		Name:                   r.Name,
		DisplayName:            derefStr(r.DisplayName),
		Transport:              r.Transport,
		Command:                derefStr(r.Command),
		Args:                   json.RawMessage(r.Args),
		URL:                    derefStr(r.URL),
		Headers:                json.RawMessage(r.Headers),
		Env:                    json.RawMessage(r.Env),
		APIKey:                 derefStr(r.APIKey),
		ToolPrefix:             derefStr(r.ToolPrefix),
		TimeoutSec:             r.TimeoutSec,
		Settings:               json.RawMessage(r.Settings),
		Enabled:                r.Enabled,
		RequireUserCredentials: r.RequireUserCredentials,
		CreatedBy:              r.CreatedBy,
	}
}
