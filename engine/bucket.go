package engine

import "hash/fnv"

// bucket places a caller in one of 100 buckets for a flag: FNV-1a 64 of
// the flag name, a NUL separator and the canonical attribute value, mod
// 100. A caller is in a rollout when bucket < percentage, so raising the
// percentage only adds callers. Changing this function reshuffles every
// running rollout; the golden test pins it.
func bucket(flagName string, v any) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(flagName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(canonical(v)))
	return h.Sum64() % 100
}
