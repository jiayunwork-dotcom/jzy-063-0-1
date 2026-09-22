package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"configplat/internal/model"
)

// Postgres is the production Store backed by PostgreSQL 16.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres connects (DSN like postgres://user:pass@host:5432/db)
// and verifies reachability.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析数据库 DSN 失败: %w", err)
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *Postgres) Close() error                   { p.pool.Close(); return nil }

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

// Schema is the DDL applied at startup.
const Schema = `
CREATE TABLE IF NOT EXISTS tenants (
	id BIGSERIAL PRIMARY KEY,
	code TEXT NOT NULL UNIQUE,
	name TEXT NOT NULL,
	namespace_quota INT NOT NULL DEFAULT 10,
	config_item_quota INT NOT NULL DEFAULT 200,
	version_retention INT NOT NULL DEFAULT 50,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS namespaces (
	id BIGSERIAL PRIMARY KEY,
	tenant_id BIGINT NOT NULL REFERENCES tenants(id),
	code TEXT NOT NULL,
	name TEXT NOT NULL,
	envs JSONB NOT NULL DEFAULT '["dev","staging","prod"]'::jsonb,
	is_public BOOLEAN NOT NULL DEFAULT FALSE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (tenant_id, code)
);
CREATE TABLE IF NOT EXISTS groups (
	id BIGSERIAL PRIMARY KEY,
	tenant_id BIGINT NOT NULL REFERENCES tenants(id),
	namespace_id BIGINT NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
	code TEXT NOT NULL,
	name TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (tenant_id, namespace_id, code)
);
CREATE TABLE IF NOT EXISTS config_items (
	id BIGSERIAL PRIMARY KEY,
	tenant_id BIGINT NOT NULL REFERENCES tenants(id),
	namespace_id BIGINT NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
	group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	key TEXT NOT NULL,
	format TEXT NOT NULL,

	values JSONB NOT NULL DEFAULT '{}'::jsonb,
	schemas JSONB NOT NULL DEFAULT '{}'::jsonb,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (tenant_id, namespace_id, group_id, key)
);
CREATE TABLE IF NOT EXISTS item_versions (
	id BIGSERIAL PRIMARY KEY,
	tenant_id BIGINT NOT NULL REFERENCES tenants(id),
	item_id BIGINT NOT NULL REFERENCES config_items(id) ON DELETE CASCADE,
	version INT NOT NULL,
	values JSONB NOT NULL,
	schemas JSONB NOT NULL DEFAULT '{}'::jsonb,
	operator TEXT NOT NULL DEFAULT '',
	change_type TEXT NOT NULL,
	base_version INT NOT NULL DEFAULT 0,
	note TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	UNIQUE (tenant_id, item_id, version)
);
CREATE INDEX IF NOT EXISTS idx_versions_item ON item_versions(tenant_id, item_id, version DESC);
CREATE TABLE IF NOT EXISTS releases (
	id BIGSERIAL PRIMARY KEY,
	tenant_id BIGINT NOT NULL REFERENCES tenants(id),
	namespace_id BIGINT NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
	env TEXT NOT NULL,
	revision BIGINT NOT NULL,
	payload JSONB NOT NULL,
	canary JSONB,
	status TEXT NOT NULL,
	stable_revision BIGINT NOT NULL DEFAULT 0,
	operator TEXT NOT NULL DEFAULT '',
	note TEXT NOT NULL DEFAULT '',
	started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	promoted_at TIMESTAMPTZ,
	rolled_back_at TIMESTAMPTZ,
	UNIQUE (tenant_id, namespace_id, env, revision)
);
CREATE INDEX IF NOT EXISTS idx_releases_lookup ON releases(tenant_id, namespace_id, env, revision DESC);
CREATE TABLE IF NOT EXISTS push_records (
	id BIGSERIAL PRIMARY KEY,
	tenant_id BIGINT NOT NULL REFERENCES tenants(id),
	namespace_id BIGINT NOT NULL REFERENCES namespaces(id) ON DELETE CASCADE,
	revision BIGINT NOT NULL,
	env TEXT NOT NULL,
	kind TEXT NOT NULL,
	operator TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_push_records_lookup ON push_records(tenant_id, namespace_id, created_at DESC);
`

// Migrate applies the schema.
func (p *Postgres) Migrate(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, Schema)
	return err
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ---- tenants ----

func (p *Postgres) CreateTenant(ctx context.Context, t *model.Tenant) (*model.Tenant, error) {
	row := p.pool.QueryRow(ctx, `
		INSERT INTO tenants(code,name,namespace_quota,config_item_quota,version_retention)
		VALUES($1,$2,$3,$4,$5) RETURNING id,created_at`,
		t.Code, t.Name, t.NamespaceQuota, t.ConfigItemQuota, t.VersionRetention)
	if err := row.Scan(&t.ID, &t.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return t, nil
}

func (p *Postgres) GetTenant(ctx context.Context, id int64) (*model.Tenant, error) {
	t := &model.Tenant{}
	err := p.pool.QueryRow(ctx,
		`SELECT id,code,name,namespace_quota,config_item_quota,version_retention,created_at
		 FROM tenants WHERE id=$1`, id).
		Scan(&t.ID, &t.Code, &t.Name, &t.NamespaceQuota, &t.ConfigItemQuota, &t.VersionRetention, &t.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return t, nil
}

func (p *Postgres) ListTenants(ctx context.Context) ([]*model.Tenant, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT id,code,name,namespace_quota,config_item_quota,version_retention,created_at
		 FROM tenants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Tenant
	for rows.Next() {
		t := &model.Tenant{}
		if err := rows.Scan(&t.ID, &t.Code, &t.Name, &t.NamespaceQuota, &t.ConfigItemQuota, &t.VersionRetention, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (p *Postgres) CountNamespaces(ctx context.Context, tenantID int64) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM namespaces WHERE tenant_id=$1 AND is_public=FALSE`, tenantID).Scan(&n)
	return n, err
}

func (p *Postgres) CountItems(ctx context.Context, tenantID int64) (int, error) {
	var n int
	err := p.pool.QueryRow(ctx,
		`SELECT count(*) FROM config_items WHERE tenant_id=$1`, tenantID).Scan(&n)
	return n, err
}

func (p *Postgres) PruneVersions(ctx context.Context, tenantID, itemID int64, keep int) error {
	_, err := p.pool.Exec(ctx, `
		DELETE FROM item_versions v
		USING (
			SELECT id FROM item_versions
			WHERE tenant_id=$1 AND item_id=$2
			ORDER BY version DESC OFFSET $3
		) old
		WHERE v.id = old.id`, tenantID, itemID, keep)
	return err
}

// ---- namespaces ----

func (p *Postgres) CreateNamespace(ctx context.Context, n *model.Namespace) (*model.Namespace, error) {
	envs := n.Envs
	if len(envs) == 0 {
		envs = model.DefaultEnvironments
	}
	row := p.pool.QueryRow(ctx, `
		INSERT INTO namespaces(tenant_id,code,name,envs,is_public)
		VALUES($1,$2,$3,$4,$5) RETURNING id,created_at`,
		n.TenantID, n.Code, n.Name, mustJSON(envs), n.IsPublic)
	if err := row.Scan(&n.ID, &n.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	n.Envs = envs
	return n, nil
}

func scanNamespace(row pgx.Row) (*model.Namespace, error) {
	n := &model.Namespace{}
	var envsRaw []byte
	err := row.Scan(&n.ID, &n.TenantID, &n.Code, &n.Name, &envsRaw, &n.IsPublic, &n.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	if len(envsRaw) > 0 {
		_ = json.Unmarshal(envsRaw, &n.Envs)
	}
	return n, nil
}

const nsCols = `id,tenant_id,code,name,envs,is_public,created_at`

func (p *Postgres) GetNamespace(ctx context.Context, id int64) (*model.Namespace, error) {
	return scanNamespace(p.pool.QueryRow(ctx,
		`SELECT `+nsCols+` FROM namespaces WHERE id=$1`, id))
}

func (p *Postgres) GetNamespaceByCode(ctx context.Context, tenantID int64, code string) (*model.Namespace, error) {
	return scanNamespace(p.pool.QueryRow(ctx,
		`SELECT `+nsCols+` FROM namespaces WHERE tenant_id=$1 AND code=$2`, tenantID, code))
}

func (p *Postgres) ListNamespaces(ctx context.Context, tenantID int64) ([]*model.Namespace, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT `+nsCols+` FROM namespaces WHERE tenant_id=$1 ORDER BY is_public DESC, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Namespace
	for rows.Next() {
		n, err := scanNamespace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ---- groups ----

func (p *Postgres) CreateGroup(ctx context.Context, g *model.Group) (*model.Group, error) {
	row := p.pool.QueryRow(ctx, `
		INSERT INTO groups(tenant_id,namespace_id,code,name)
		VALUES($1,$2,$3,$4) RETURNING id,created_at`,
		g.TenantID, g.NamespaceID, g.Code, g.Name)
	if err := row.Scan(&g.ID, &g.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return g, nil
}

func scanGroup(row pgx.Row) (*model.Group, error) {
	g := &model.Group{}
	err := row.Scan(&g.ID, &g.TenantID, &g.NamespaceID, &g.Code, &g.Name, &g.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return g, nil
}

const grpCols = `id,tenant_id,namespace_id,code,name,created_at`

func (p *Postgres) GetGroup(ctx context.Context, id int64) (*model.Group, error) {
	return scanGroup(p.pool.QueryRow(ctx,
		`SELECT `+grpCols+` FROM groups WHERE id=$1`, id))
}

func (p *Postgres) GetGroupByCode(ctx context.Context, tenantID, namespaceID int64, code string) (*model.Group, error) {
	return scanGroup(p.pool.QueryRow(ctx,
		`SELECT `+grpCols+` FROM groups WHERE tenant_id=$1 AND namespace_id=$2 AND code=$3`,
		tenantID, namespaceID, code))
}

func (p *Postgres) ListGroups(ctx context.Context, tenantID, namespaceID int64) ([]*model.Group, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT `+grpCols+` FROM groups WHERE tenant_id=$1 AND namespace_id=$2 ORDER BY id`,
		tenantID, namespaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ---- items ----

const itemCols = `id,tenant_id,namespace_id,group_id,key,format,values,schemas,created_at,updated_at`

func scanItem(row pgx.Row) (*model.ConfigItem, error) {
	it := &model.ConfigItem{Values: map[string]string{}, Schemas: map[string]string{}}
	var valuesRaw, schemasRaw []byte
	err := row.Scan(&it.ID, &it.TenantID, &it.NamespaceID, &it.GroupID, &it.Key,
		&it.Format, &valuesRaw, &schemasRaw, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	_ = json.Unmarshal(valuesRaw, &it.Values)
	_ = json.Unmarshal(schemasRaw, &it.Schemas)
	return it, nil
}

func (p *Postgres) CreateItem(ctx context.Context, item *model.ConfigItem) (*model.ConfigItem, error) {
	row := p.pool.QueryRow(ctx, `
		INSERT INTO config_items(tenant_id,namespace_id,group_id,key,format,values,schemas)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id,created_at,updated_at`,
		item.TenantID, item.NamespaceID, item.GroupID, item.Key, item.Format,
		mustJSON(item.Values), mustJSON(item.Schemas))
	if err := row.Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return nil, mapErr(err)
	}
	return item, nil
}

func (p *Postgres) UpdateItem(ctx context.Context, item *model.ConfigItem) error {
	ct, err := p.pool.Exec(ctx, `
		UPDATE config_items SET format=$1,values=$2,schemas=$3,updated_at=now()
		WHERE id=$4 AND tenant_id=$5`,
		item.Format, mustJSON(item.Values), mustJSON(item.Schemas), item.ID, item.TenantID)
	if err != nil {
		return mapErr(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	item.UpdatedAt = time.Now()
	return nil
}

func (p *Postgres) GetItem(ctx context.Context, id int64) (*model.ConfigItem, error) {
	return scanItem(p.pool.QueryRow(ctx,
		`SELECT `+itemCols+` FROM config_items WHERE id=$1`, id))
}

func (p *Postgres) GetItemByKey(ctx context.Context, tenantID, namespaceID, groupID int64, key string) (*model.ConfigItem, error) {
	return scanItem(p.pool.QueryRow(ctx,
		`SELECT `+itemCols+` FROM config_items
		 WHERE tenant_id=$1 AND namespace_id=$2 AND group_id=$3 AND key=$4`,
		tenantID, namespaceID, groupID, key))
}

func (p *Postgres) listItems(ctx context.Context, query string, args ...any) ([]*model.ConfigItem, error) {
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ConfigItem
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (p *Postgres) ListItems(ctx context.Context, tenantID, namespaceID, groupID int64) ([]*model.ConfigItem, error) {
	return p.listItems(ctx,
		`SELECT `+itemCols+` FROM config_items
		 WHERE tenant_id=$1 AND namespace_id=$2 AND group_id=$3 ORDER BY key`,
		tenantID, namespaceID, groupID)
}

func (p *Postgres) ListNamespaceItems(ctx context.Context, tenantID, namespaceID int64) ([]*model.ConfigItem, error) {
	return p.listItems(ctx,
		`SELECT `+itemCols+` FROM config_items
		 WHERE tenant_id=$1 AND namespace_id=$2 ORDER BY group_id,key`,
		tenantID, namespaceID)
}

// ---- versions ----

const verCols = `id,tenant_id,item_id,version,values,schemas,operator,change_type,base_version,note,created_at`

func scanVersion(row pgx.Row) (*model.ItemVersion, error) {
	v := &model.ItemVersion{Values: map[string]string{}, Schemas: map[string]string{}}
	var valuesRaw, schemasRaw []byte
	err := row.Scan(&v.ID, &v.TenantID, &v.ItemID, &v.Version, &valuesRaw, &schemasRaw,
		&v.Operator, &v.ChangeType, &v.BaseVersion, &v.Note, &v.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	_ = json.Unmarshal(valuesRaw, &v.Values)
	_ = json.Unmarshal(schemasRaw, &v.Schemas)
	return v, nil
}

func (p *Postgres) AddVersion(ctx context.Context, v *model.ItemVersion) (*model.ItemVersion, error) {
	row := p.pool.QueryRow(ctx, `
		INSERT INTO item_versions(tenant_id,item_id,version,values,schemas,operator,change_type,base_version,note)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id,created_at`,
		v.TenantID, v.ItemID, v.Version, mustJSON(v.Values), mustJSON(v.Schemas),
		v.Operator, v.ChangeType, v.BaseVersion, v.Note)
	if err := row.Scan(&v.ID, &v.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	return v, nil
}

func (p *Postgres) ListVersions(ctx context.Context, tenantID, itemID int64) ([]*model.ItemVersion, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT `+verCols+` FROM item_versions
		 WHERE tenant_id=$1 AND item_id=$2 ORDER BY version DESC`, tenantID, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.ItemVersion
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) GetVersion(ctx context.Context, tenantID, itemID int64, version int) (*model.ItemVersion, error) {
	return scanVersion(p.pool.QueryRow(ctx,
		`SELECT `+verCols+` FROM item_versions
		 WHERE tenant_id=$1 AND item_id=$2 AND version=$3`, tenantID, itemID, version))
}

// ---- releases ----

const relCols = `id,tenant_id,namespace_id,env,revision,payload,canary,status,stable_revision,operator,note,started_at,promoted_at,rolled_back_at`

func scanRelease(row pgx.Row) (*model.Release, error) {
	r := &model.Release{}
	var payloadRaw, canaryRaw []byte
	var promotedAt, rolledBackAt *time.Time
	err := row.Scan(&r.ID, &r.TenantID, &r.NamespaceID, &r.Env, &r.Revision,
		&payloadRaw, &canaryRaw, &r.Status, &r.StableRevision, &r.Operator, &r.Note,
		&r.StartedAt, &promotedAt, &rolledBackAt)
	if err != nil {
		return nil, mapErr(err)
	}
	if len(payloadRaw) > 0 {
		r.Payload = &model.Payload{
			Groups:  map[string]map[string]string{},
			Sources: map[string]map[string]model.KeySource{},
		}
		_ = json.Unmarshal(payloadRaw, r.Payload)
	}
	if len(canaryRaw) > 0 && string(canaryRaw) != "null" {
		r.Canary = &model.Canary{}
		_ = json.Unmarshal(canaryRaw, r.Canary)
	}
	r.PromotedAt = promotedAt
	r.RolledBackAt = rolledBackAt
	return r, nil
}

func (p *Postgres) CreateRelease(ctx context.Context, r *model.Release) (*model.Release, error) {
	var canaryVal any
	if r.Canary != nil {
		canaryVal = mustJSON(r.Canary)
	}
	row := p.pool.QueryRow(ctx, `
		INSERT INTO releases(tenant_id,namespace_id,env,revision,payload,canary,status,stable_revision,operator,note,started_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id,started_at`,
		r.TenantID, r.NamespaceID, r.Env, r.Revision, mustJSON(r.Payload), canaryVal,
		r.Status, r.StableRevision, r.Operator, r.Note, r.StartedAt)
	if err := row.Scan(&r.ID, &r.StartedAt); err != nil {
		return nil, mapErr(err)
	}
	return r, nil
}

func (p *Postgres) UpdateRelease(ctx context.Context, r *model.Release) error {
	var canaryVal any
	if r.Canary != nil {
		canaryVal = mustJSON(r.Canary)
	}
	ct, err := p.pool.Exec(ctx, `
		UPDATE releases SET payload=$1,canary=$2,status=$3,stable_revision=$4,
			promoted_at=$5,rolled_back_at=$6,note=$7
		WHERE id=$8 AND tenant_id=$9`,
		mustJSON(r.Payload), canaryVal, r.Status, r.StableRevision,
		r.PromotedAt, r.RolledBackAt, r.Note, r.ID, r.TenantID)
	if err != nil {
		return mapErr(err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) GetRelease(ctx context.Context, id int64) (*model.Release, error) {
	return scanRelease(p.pool.QueryRow(ctx,
		`SELECT `+relCols+` FROM releases WHERE id=$1`, id))
}

func (p *Postgres) GetReleaseByRevision(ctx context.Context, tenantID, namespaceID int64, env string, revision int64) (*model.Release, error) {
	return scanRelease(p.pool.QueryRow(ctx,
		`SELECT `+relCols+` FROM releases
		 WHERE tenant_id=$1 AND namespace_id=$2 AND env=$3 AND revision=$4`,
		tenantID, namespaceID, env, revision))
}

func (p *Postgres) LatestRelease(ctx context.Context, tenantID, namespaceID int64, env string) (*model.Release, error) {
	return scanRelease(p.pool.QueryRow(ctx,
		`SELECT `+relCols+` FROM releases
		 WHERE tenant_id=$1 AND namespace_id=$2 AND env=$3 ORDER BY revision DESC LIMIT 1`,
		tenantID, namespaceID, env))
}

func (p *Postgres) ListReleases(ctx context.Context, tenantID, namespaceID int64, env string, limit int) ([]*model.Release, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx,
		`SELECT `+relCols+` FROM releases
		 WHERE tenant_id=$1 AND namespace_id=$2 AND env=$3 ORDER BY revision DESC LIMIT $4`,
		tenantID, namespaceID, env, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Release
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ---- push records ----

func (p *Postgres) AddPushRecord(ctx context.Context, tenantID, namespaceID int64, r *model.PushRecord) error {
	return p.pool.QueryRow(ctx, `
		INSERT INTO push_records(tenant_id,namespace_id,revision,env,kind,operator,status,note)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
		tenantID, namespaceID, r.Revision, r.Env, r.Kind, r.Operator, r.Status, r.Note).
		Scan(&r.CreatedAt)
}

func (p *Postgres) ListPushRecords(ctx context.Context, tenantID, namespaceID int64, limit int) ([]*model.PushRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := p.pool.Query(ctx,
		`SELECT revision,env,kind,operator,status,note,created_at
		 FROM push_records WHERE tenant_id=$1 AND namespace_id=$2
		 ORDER BY created_at DESC LIMIT $3`, tenantID, namespaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.PushRecord
	for rows.Next() {
		r := &model.PushRecord{}
		if err := rows.Scan(&r.Revision, &r.Env, &r.Kind, &r.Operator, &r.Status, &r.Note, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
