// Package store defines persistence for tenants, namespaces, groups, config
// items, immutable versions and releases. Two implementations are provided:
// Memory (for tests / local runs) and Postgres (production).
package store

import (
	"context"
	"errors"

	"configplat/internal/model"
)

var (
	// ErrNotFound is returned when no row matches.
	ErrNotFound = errors.New("记录不存在")
	// ErrConflict is returned on unique-key violations.
	ErrConflict = errors.New("记录已存在或发生唯一键冲突")
)

// Store is the full persistence boundary of the platform.
type Store interface {
	// tenants & quota
	CreateTenant(ctx context.Context, t *model.Tenant) (*model.Tenant, error)
	GetTenant(ctx context.Context, id int64) (*model.Tenant, error)
	ListTenants(ctx context.Context) ([]*model.Tenant, error)
	CountNamespaces(ctx context.Context, tenantID int64) (int, error)
	CountItems(ctx context.Context, tenantID int64) (int, error)
	PruneVersions(ctx context.Context, tenantID, itemID int64, keep int) error

	// namespaces
	CreateNamespace(ctx context.Context, n *model.Namespace) (*model.Namespace, error)
	GetNamespace(ctx context.Context, id int64) (*model.Namespace, error)
	GetNamespaceByCode(ctx context.Context, tenantID int64, code string) (*model.Namespace, error)
	ListNamespaces(ctx context.Context, tenantID int64) ([]*model.Namespace, error)

	// groups
	CreateGroup(ctx context.Context, g *model.Group) (*model.Group, error)
	GetGroup(ctx context.Context, id int64) (*model.Group, error)
	GetGroupByCode(ctx context.Context, tenantID, namespaceID int64, code string) (*model.Group, error)
	ListGroups(ctx context.Context, tenantID, namespaceID int64) ([]*model.Group, error)

	// config items
	CreateItem(ctx context.Context, item *model.ConfigItem) (*model.ConfigItem, error)
	UpdateItem(ctx context.Context, item *model.ConfigItem) error
	GetItem(ctx context.Context, id int64) (*model.ConfigItem, error)
	GetItemByKey(ctx context.Context, tenantID, namespaceID, groupID int64, key string) (*model.ConfigItem, error)
	ListItems(ctx context.Context, tenantID, namespaceID, groupID int64) ([]*model.ConfigItem, error)
	ListNamespaceItems(ctx context.Context, tenantID, namespaceID int64) ([]*model.ConfigItem, error)

	// versions
	AddVersion(ctx context.Context, v *model.ItemVersion) (*model.ItemVersion, error)
	ListVersions(ctx context.Context, tenantID, itemID int64) ([]*model.ItemVersion, error)
	GetVersion(ctx context.Context, tenantID, itemID int64, version int) (*model.ItemVersion, error)

	// releases
	CreateRelease(ctx context.Context, r *model.Release) (*model.Release, error)
	UpdateRelease(ctx context.Context, r *model.Release) error
	GetRelease(ctx context.Context, id int64) (*model.Release, error)
	GetReleaseByRevision(ctx context.Context, tenantID, namespaceID int64, env string, revision int64) (*model.Release, error)
	LatestRelease(ctx context.Context, tenantID, namespaceID int64, env string) (*model.Release, error)
	ListReleases(ctx context.Context, tenantID, namespaceID int64, env string, limit int) ([]*model.Release, error)

	// push records (board)
	AddPushRecord(ctx context.Context, tenantID, namespaceID int64, r *model.PushRecord) error
	ListPushRecords(ctx context.Context, tenantID, namespaceID int64, limit int) ([]*model.PushRecord, error)

	Ping(ctx context.Context) error
	Close() error
}
