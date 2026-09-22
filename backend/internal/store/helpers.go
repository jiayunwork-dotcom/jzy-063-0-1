package store

import (
	"sort"

	"configplat/internal/model"
)

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneTenant(t *model.Tenant) *model.Tenant {
	cp := *t
	return &cp
}

func cloneNamespace(n *model.Namespace) *model.Namespace {
	cp := *n
	cp.Envs = cloneStringSlice(n.Envs)
	return &cp
}

func cloneGroup(g *model.Group) *model.Group {
	cp := *g
	return &cp
}

func cloneItem(it *model.ConfigItem) *model.ConfigItem {
	cp := *it
	cp.Values = cloneStringMap(it.Values)
	cp.Schemas = cloneStringMap(it.Schemas)
	return &cp
}

func cloneVersion(v *model.ItemVersion) *model.ItemVersion {
	cp := *v
	cp.Values = cloneStringMap(v.Values)
	cp.Schemas = cloneStringMap(v.Schemas)
	return &cp
}

func cloneRelease(r *model.Release) *model.Release {
	cp := *r
	if r.Canary != nil {
		c := *r.Canary
		c.IPs = cloneStringSlice(r.Canary.IPs)
		c.Selection = cloneStringSlice(r.Canary.Selection)
		cp.Canary = &c
	}
	if r.Payload != nil {
		p := model.Payload{
			Groups:  map[string]map[string]string{},
			Sources: map[string]map[string]model.KeySource{},
		}
		for g, kv := range r.Payload.Groups {
			p.Groups[g] = cloneStringMap(kv)
		}
		for g, kv := range r.Payload.Sources {
			p.Sources[g] = map[string]model.KeySource{}
			for k, ks := range kv {
				ks2 := model.KeySource{Source: ks.Source}
				if ks.Paths != nil {
					ks2.Paths = cloneStringMap(ks.Paths)
				}
				p.Sources[g][k] = ks2
			}
		}
		cp.Payload = &p
	}
	if r.PromotedAt != nil {
		t := *r.PromotedAt
		cp.PromotedAt = &t
	}
	if r.RolledBackAt != nil {
		t := *r.RolledBackAt
		cp.RolledBackAt = &t
	}
	return &cp
}

func sortTenants(ts []*model.Tenant) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
}

func sortNamespaces(ns []*model.Namespace) {
	sort.Slice(ns, func(i, j int) bool { return ns[i].ID < ns[j].ID })
}

func sortGroups(gs []*model.Group) {
	sort.Slice(gs, func(i, j int) bool { return gs[i].ID < gs[j].ID })
}

func sortItems(items []*model.ConfigItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].GroupID != items[j].GroupID {
			return items[i].GroupID < items[j].GroupID
		}
		return items[i].Key < items[j].Key
	})
}

func sortVersionsAsc(vs []*model.ItemVersion) {
	sort.Slice(vs, func(i, j int) bool { return vs[i].Version < vs[j].Version })
}

func sortVersionsDesc(vs []*model.ItemVersion) {
	sort.Slice(vs, func(i, j int) bool { return vs[i].Version > vs[j].Version })
}

func sortReleasesDesc(rs []*model.Release) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Revision > rs[j].Revision })
}

func sortPushDesc(rs []*model.PushRecord) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].CreatedAt.After(rs[j].CreatedAt) })
}
