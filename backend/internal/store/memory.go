package store

import (
	"context"
	"sync"
	"time"

	"configplat/internal/model"
)

// Memory is an in-process Store used by unit tests.
type Memory struct {
	mu sync.Mutex

	tenants     map[int64]*model.Tenant
	namespaces  map[int64]*model.Namespace
	groups      map[int64]*model.Group
	items       map[int64]*model.ConfigItem
	versions    map[int64]*model.ItemVersion // keyed by version row id
	releases    map[int64]*model.Release
	pushRecords map[int64]pushRec

	tenantSeq    int64
	namespaceSeq int64
	groupSeq     int64
	itemSeq      int64
	versionSeq   int64
	releaseSeq   int64
	pushSeq      int64
}

type pushRec struct {
	tenantID    int64
	namespaceID int64
	rec         *model.PushRecord
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		tenants:     map[int64]*model.Tenant{},
		namespaces:  map[int64]*model.Namespace{},
		groups:      map[int64]*model.Group{},
		items:       map[int64]*model.ConfigItem{},
		versions:    map[int64]*model.ItemVersion{},
		releases:    map[int64]*model.Release{},
		pushRecords: map[int64]*pushRec{},
	}
}

func (m *Memory) Ping(context.Context) error { return nil }
func (m *Memory) Close() error               { return nil }

func (m *Memory) CreateTenant(_ context.Context, t *model.Tenant) (*model.Tenant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tenantSeq++
	t.ID = m.tenantSeq
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	m.tenants[t.ID] = cloneTenant(t)
	return cloneTenant(t), nil
}

func (m *Memory) GetTenant(_ context.Context, id int64) (*model.Tenant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tenants[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneTenant(t), nil
}

func (m *Memory) ListTenants(_ context.Context) ([]*model.Tenant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.Tenant
	for _, t := range m.tenants {
		out = append(out, cloneTenant(t))
	}
	sortTenants(out)
	return out, nil
}

func (m *Memory) CountNamespaces(_ context.Context, tenantID int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, ns := range m.namespaces {
		if ns.TenantID == tenantID && !ns.IsPublic {
			n++
		}
	}
	return n, nil
}

func (m *Memory) CountItems(_ context.Context, tenantID int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, it := range m.items {
		if it.TenantID == tenantID {
			n++
		}
	}
	return n, nil
}

func (m *Memory) PruneVersions(_ context.Context, tenantID, itemID int64, keep int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var vs []*model.ItemVersion
	for _, v := range m.versions {
		if v.TenantID == tenantID && v.ItemID == itemID {
			vs = append(vs, v)
		}
	}
	if len(vs) <= keep {
		return nil
	}
	// ascending by version; delete oldest, keep the latest `keep`
	sortVersionsAsc(vs)
	drop := len(vs) - keep
	for i := 0; i < drop; i++ {
		delete(m.versions, vs[i].ID)
	}
	return nil
}

func (m *Memory) CreateNamespace(_ context.Context, n *model.Namespace) (*model.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range m.namespaces {
		if ex.TenantID == n.TenantID && ex.Code == n.Code {
			return nil, ErrConflict
		}
	}
	m.namespaceSeq++
	n.ID = m.namespaceSeq
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now()
	}
	if len(n.Envs) == 0 {
		n.Envs = append([]string(nil), model.DefaultEnvironments...)
	}
	m.namespaces[n.ID] = cloneNamespace(n)
	return cloneNamespace(n), nil
}

func (m *Memory) GetNamespace(_ context.Context, id int64) (*model.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.namespaces[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneNamespace(n), nil
}

func (m *Memory) GetNamespaceByCode(_ context.Context, tenantID int64, code string) (*model.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range m.namespaces {
		if n.TenantID == tenantID && n.Code == code {
			return cloneNamespace(n), nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) ListNamespaces(_ context.Context, tenantID int64) ([]*model.Namespace, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.Namespace
	for _, n := range m.namespaces {
		if n.TenantID == tenantID {
			out = append(out, cloneNamespace(n))
		}
	}
	sortNamespaces(out)
	return out, nil
}

func (m *Memory) CreateGroup(_ context.Context, g *model.Group) (*model.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range m.groups {
		if ex.TenantID == g.TenantID && ex.NamespaceID == g.NamespaceID && ex.Code == g.Code {
			return nil, ErrConflict
		}
	}
	m.groupSeq++
	g.ID = m.groupSeq
	if g.CreatedAt.IsZero() {
		g.CreatedAt = time.Now()
	}
	m.groups[g.ID] = cloneGroup(g)
	return cloneGroup(g), nil
}

func (m *Memory) GetGroup(_ context.Context, id int64) (*model.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.groups[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneGroup(g), nil
}

func (m *Memory) GetGroupByCode(_ context.Context, tenantID, namespaceID int64, code string) (*model.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, g := range m.groups {
		if g.TenantID == tenantID && g.NamespaceID == namespaceID && g.Code == code {
			return cloneGroup(g), nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) ListGroups(_ context.Context, tenantID, namespaceID int64) ([]*model.Group, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.Group
	for _, g := range m.groups {
		if g.TenantID == tenantID && g.NamespaceID == namespaceID {
			out = append(out, cloneGroup(g))
		}
	}
	sortGroups(out)
	return out, nil
}

func (m *Memory) CreateItem(_ context.Context, item *model.ConfigItem) (*model.ConfigItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range m.items {
		if ex.TenantID == item.TenantID && ex.NamespaceID == item.NamespaceID &&
			ex.GroupID == item.GroupID && ex.Key == item.Key {
			return nil, ErrConflict
		}
	}
	m.itemSeq++
	item.ID = m.itemSeq
	now := time.Now()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	m.items[item.ID] = cloneItem(item)
	return cloneItem(item), nil
}

func (m *Memory) UpdateItem(_ context.Context, item *model.ConfigItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.items[item.ID]; !ok {
		return ErrNotFound
	}
	item.UpdatedAt = time.Now()
	m.items[item.ID] = cloneItem(item)
	return nil
}

func (m *Memory) GetItem(_ context.Context, id int64) (*model.ConfigItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.items[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneItem(it), nil
}

func (m *Memory) GetItemByKey(_ context.Context, tenantID, namespaceID, groupID int64, key string) (*model.ConfigItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if it.TenantID == tenantID && it.NamespaceID == namespaceID &&
			it.GroupID == groupID && it.Key == key {
			return cloneItem(it), nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) ListItems(_ context.Context, tenantID, namespaceID, groupID int64) ([]*model.ConfigItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.ConfigItem
	for _, it := range m.items {
		if it.TenantID == tenantID && it.NamespaceID == namespaceID && it.GroupID == groupID {
			out = append(out, cloneItem(it))
		}
	}
	sortItems(out)
	return out, nil
}

func (m *Memory) ListNamespaceItems(_ context.Context, tenantID, namespaceID int64) ([]*model.ConfigItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.ConfigItem
	for _, it := range m.items {
		if it.TenantID == tenantID && it.NamespaceID == namespaceID {
			out = append(out, cloneItem(it))
		}
	}
	sortItems(out)
	return out, nil
}

func (m *Memory) AddVersion(_ context.Context, v *model.ItemVersion) (*model.ItemVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range m.versions {
		if ex.TenantID == v.TenantID && ex.ItemID == v.ItemID && ex.Version == v.Version {
			return nil, ErrConflict
		}
	}
	m.versionSeq++
	v.ID = m.versionSeq
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	m.versions[v.ID] = cloneVersion(v)
	return cloneVersion(v), nil
}

func (m *Memory) ListVersions(_ context.Context, tenantID, itemID int64) ([]*model.ItemVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.ItemVersion
	for _, v := range m.versions {
		if v.TenantID == tenantID && v.ItemID == itemID {
			out = append(out, cloneVersion(v))
		}
	}
	sortVersionsDesc(out)
	return out, nil
}

func (m *Memory) GetVersion(_ context.Context, tenantID, itemID int64, version int) (*model.ItemVersion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.versions {
		if v.TenantID == tenantID && v.ItemID == itemID && v.Version == version {
			return cloneVersion(v), nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) CreateRelease(_ context.Context, r *model.Release) (*model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ex := range m.releases {
		if ex.TenantID == r.TenantID && ex.NamespaceID == r.NamespaceID &&
			ex.Env == r.Env && ex.Revision == r.Revision {
			return nil, ErrConflict
		}
	}
	m.releaseSeq++
	r.ID = m.releaseSeq
	if r.StartedAt.IsZero() {
		r.StartedAt = time.Now()
	}
	m.releases[r.ID] = cloneRelease(r)
	return cloneRelease(r), nil
}

func (m *Memory) UpdateRelease(_ context.Context, r *model.Release) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.releases[r.ID]; !ok {
		return ErrNotFound
	}
	m.releases[r.ID] = cloneRelease(r)
	return nil
}

func (m *Memory) GetRelease(_ context.Context, id int64) (*model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.releases[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneRelease(r), nil
}

func (m *Memory) GetReleaseByRevision(_ context.Context, tenantID, namespaceID int64, env string, revision int64) (*model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.releases {
		if r.TenantID == tenantID && r.NamespaceID == namespaceID &&
			r.Env == env && r.Revision == revision {
			return cloneRelease(r), nil
		}
	}
	return nil, ErrNotFound
}

func (m *Memory) LatestRelease(_ context.Context, tenantID, namespaceID int64, env string) (*model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *model.Release
	for _, r := range m.releases {
		if r.TenantID == tenantID && r.NamespaceID == namespaceID && r.Env == env {
			if best == nil || r.Revision > best.Revision {
				best = r
			}
		}
	}
	if best == nil {
		return nil, ErrNotFound
	}
	return cloneRelease(best), nil
}

func (m *Memory) ListReleases(_ context.Context, tenantID, namespaceID int64, env string, limit int) ([]*model.Release, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.Release
	for _, r := range m.releases {
		if r.TenantID == tenantID && r.NamespaceID == namespaceID && r.Env == env {
			out = append(out, cloneRelease(r))
		}
	}
	sortReleasesDesc(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) AddPushRecord(_ context.Context, tenantID, namespaceID int64, r *model.PushRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushSeq++
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	m.pushRecords[m.pushSeq] = &pushRec{tenantID: tenantID, namespaceID: namespaceID, rec: r}
	return nil
}

func (m *Memory) ListPushRecords(_ context.Context, tenantID, namespaceID int64, limit int) ([]*model.PushRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*model.PushRecord
	for _, p := range m.pushRecords {
		if p.tenantID == tenantID && p.namespaceID == namespaceID {
			cp := *p.rec
			out = append(out, &cp)
		}
	}
	sortPushDesc(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
