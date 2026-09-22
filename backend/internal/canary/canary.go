// Package canary implements gray-release target selection:
// explicit IP allow-list or stable pseudo-random percentage selection.
//
// Percentage selection is deterministic per (seed, ip): the same instance
// makes the same decision for the lifetime of a release even if it
// reconnects, while the aggregate over many instances converges to the
// requested percentage (verifiable in tests).
package canary

import (
	"hash/fnv"
	"sort"

	"configplat/internal/model"
)

// SelectByIPList returns the subset of candidate IPs that appear in the
// allow-list. Candidates are the instances currently connected; list entries
// not connected simply wait until they connect (Match handles that).
func SelectByIPList(candidates []string, allowList []string) []string {
	want := map[string]bool{}
	for _, ip := range allowList {
		want[ip] = true
	}
	var picked []string
	seen := map[string]bool{}
	for _, ip := range candidates {
		if want[ip] && !seen[ip] {
			picked = append(picked, ip)
			seen[ip] = true
		}
	}
	sort.Strings(picked)
	return picked
}

// SelectByPercent deterministically picks approximately percent% of the
// candidate IPs. seed should identify the release (e.g. namespace id XOR
// revision) so that membership is stable for that release. The returned
// count is round(len*percent/100) of candidates, chosen by per-IP hash.
func SelectByPercent(candidates []string, percent int, seed int64) []string {
	if percent <= 0 || len(candidates) == 0 {
		return nil
	}
	if percent >= 100 {
		out := append([]string(nil), candidates...)
		sort.Strings(out)
		return out
	}
	type scored struct {
		ip    string
		score uint32
	}
	all := make([]scored, len(candidates))
	for i, ip := range candidates {
		all[i] = scored{ip, hashSeedIP(seed, ip)}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score < all[j].score
		}
		return all[i].ip < all[j].ip
	})
	need := len(candidates) * percent / 100
	// round to nearest instead of truncating for small fleets
	if rem := (len(candidates)*percent - need*100) * 2; rem >= 100 {
		need++
	}
	out := make([]string, 0, need)
	for i := 0; i < need; i++ {
		out = append(out, all[i].ip)
	}
	sort.Strings(out)
	return out
}

// Match reports whether an instance IP is targeted by the canary rule.
// For percentage canaries it evaluates the same per-IP hash predicate used by
// SelectByPercent, so its aggregate hit-rate is approximately percent/100.
func Match(ip string, c *model.Canary, seed int64) bool {
	if c == nil {
		return false
	}
	switch c.Type {
	case model.CanaryByIPList:
		for _, allowed := range c.IPs {
			if allowed == ip {
				return true
			}
		}
		return false
	case model.CanaryByPercent:
		if c.Percent <= 0 {
			return false
		}
		if c.Percent >= 100 {
			return true
		}
		return hashSeedIP(seed, ip)%100 < uint32(c.Percent)
	default:
		return false
	}
}

// SelectionSet converts an explicit selection list to a set.
func SelectionSet(c *model.Canary) map[string]bool {
	out := map[string]bool{}
	if c == nil {
		return out
	}
	for _, ip := range c.Selection {
		out[ip] = true
	}
	return out
}

func hashSeedIP(seed int64, ip string) uint32 {
	h := fnv.New32a()
	var b [8]byte
	for i := 0; i < 8; i++ {
		b[i] = byte(seed >> (uint(i) * 8))
	}
	_, _ = h.Write(b)
	_, _ = h.Write([]byte(ip))
	return h.Sum32()
}
