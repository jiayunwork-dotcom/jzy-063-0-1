// Package model contains the core domain entities shared across the platform.
package model

import "time"

// Formats supported by configuration values.
const (
	FormatJSON       = "json"
	FormatYAML       = "yaml"
	FormatProperties = "properties"
	FormatTOML       = "toml"
)

var SupportedFormats = map[string]bool{
	FormatJSON: true, FormatYAML: true, FormatProperties: true, FormatTOML: true,
}

// DefaultEnvironments are the environment labels a namespace starts with.
var DefaultEnvironments = []string{"dev", "staging", "prod"}

// PublicNamespaceCode / PublicGroupCode name the shared layer.
const (
	PublicNamespaceCode = "_public"
	PublicNamespaceName = "公共配置"
	PublicGroupCode     = "_public"
	PublicGroupName     = "公共配置"
)

// Layer identifiers used for provenance tracking.
const (
	LayerPublic    = "public"
	LayerNamespace = "namespace"
	LayerGroup     = "group"
)

// Tenant is an isolated customer of the platform.
type Tenant struct {
	ID                   int64     `json:"id"`
	Code                 string    `json:"code"`
	Name                 string    `json:"name"`
	NamespaceQuota       int       `json:"namespaceQuota"` // excludes the public namespace
	ConfigItemQuota      int       `json:"configItemQuota"`
	VersionRetention     int       `json:"versionRetention"`
	CreatedAt            time.Time `json:"createdAt"`
}

// Namespace corresponds to one business line.
type Namespace struct {
	ID        int64     `json:"id"`
	TenantID  int64     `json:"tenantId"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Envs      []string  `json:"envs"`
	IsPublic  bool      `json:"isPublic"`
	CreatedAt time.Time `json:"createdAt"`
}

// Group corresponds to one service module inside a namespace.
type Group struct {
	ID          int64     `json:"id"`
	TenantID    int64     `json:"tenantId"`
	NamespaceID int64     `json:"namespaceId"`
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
}

// EnvValue is one environment-specific raw configuration value.
type EnvValue struct {
	Value  string `json:"value"`
	Schema string `json:"schema,omitempty"` // optional JSON Schema
}

// ConfigItem is a single key located by (namespace, group, key).
// Values maps environment label -> raw value in the item's declared format.
// Schemas maps environment label -> optional JSON Schema.
type ConfigItem struct {
	ID          int64             `json:"id"`
	TenantID    int64             `json:"tenantId"`
	NamespaceID int64             `json:"namespaceId"`
	GroupID     int64             `json:"groupId"`
	Key         string            `json:"key"`
	Format      string            `json:"format"`
	Values      map[string]string `json:"values"`
	Schemas     map[string]string `json:"schemas,omitempty"`
	UpdatedAt   time.Time         `json:"updatedAt"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// ItemVersion is one immutable revision of a config item.
type ItemVersion struct {
	ID          int64             `json:"id"`
	TenantID    int64             `json:"tenantId"`
	ItemID      int64             `json:"itemId"`
	Version     int               `json:"version"`
	Values      map[string]string `json:"values"`
	Schemas     map[string]string `json:"schemas,omitempty"`
	Operator    string            `json:"operator"`
	ChangeType  string            `json:"changeType"` // create | update | rollback
	BaseVersion int               `json:"baseVersion,omitempty"`
	Note        string            `json:"note,omitempty"`
	CreatedAt   time.Time         `json:"createdAt"`
}

// DiffLine is one row of a line-level diff between two versions.
type DiffLine struct {
	Type   string `json:"type"` // add | remove | equal
	OldNum int    `json:"oldNum,omitempty"`
	NewNum int    `json:"newNum,omitempty"`
	Text   string `json:"text"`
}

// ReleaseStatus values.
const (
	CanaryActive   = "canary"
	StableStatus   = "stable"
	PromotedStatus = "promoted"
	RolledBack     = "rolled_back"
)

// CanaryType values.
const (
	CanaryByIPList   = "ip_list"
	CanaryByPercent  = "percent"
	CanaryFull       = "full"
)

// Canary defines the target selection of a gray release.
type Canary struct {
	Type      string   `json:"type"` // ip_list | percent
	IPs       []string `json:"ips,omitempty"`
	Percent   int      `json:"percent,omitempty"` // 1..99
	Selection []string `json:"selection,omitempty"`
}

// KeySource records the provenance of one merged config key.
type KeySource struct {
	Source string            `json:"source"`            // public | namespace | group (dominant/root layer)
	Paths  map[string]string `json:"paths,omitempty"`   // deep JSON path -> layer (deep merge)
}

// Payload is the effective configuration served to clients for one
// namespace+environment: rendered text keyed by group/key plus provenance.
type Payload struct {
	Groups  map[string]map[string]string `json:"groups"`
	Sources map[string]map[string]KeySource `json:"sources,omitempty"`
}

// Release is one namespace+environment configuration revision.
type Release struct {
	ID             int64          `json:"id"`
	TenantID       int64          `json:"tenantId"`
	NamespaceID    int64          `json:"namespaceId"`
	Env            string         `json:"env"`
	Revision       int64          `json:"revision"`
	Payload        *Payload       `json:"payload"`
	Canary         *Canary        `json:"canary,omitempty"`
	Status         string         `json:"status"`
	StableRevision int64          `json:"stableRevision"` // revision still served to non-canary instances
	Operator       string         `json:"operator"`
	Note           string         `json:"note,omitempty"`
	StartedAt      time.Time      `json:"startedAt"`
	PromotedAt     *time.Time     `json:"promotedAt,omitempty"`
	RolledBackAt   *time.Time     `json:"rolledBackAt,omitempty"`
}

// CanaryStats describes live gray-release progress for the console.
type CanaryStats struct {
	Delivered int   `json:"delivered"`
	Total     int   `json:"total"`
}

// ChangeEvent is published when a namespace revision changes.
type ChangeEvent struct {
	TenantID    int64  `json:"tenantId"`
	NamespaceID int64  `json:"namespaceId"`
	Env         string `json:"env"`
	Revision    int64  `json:"revision"`
	Kind        string `json:"kind"` // release | promote | rollback
}

// PushRecord is an entry on the "recent pushes" board.
type PushRecord struct {
	Revision  int64     `json:"revision"`
	Env       string    `json:"env"`
	Kind      string    `json:"kind"`
	Operator  string    `json:"operator"`
	Status    string    `json:"status"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}
