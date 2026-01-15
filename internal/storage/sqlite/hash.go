package sqlite

import (
	"encoding/binary"
	"hash/fnv"
)

// computeDedupHash generates a 64-bit FNV-1a hash for deduplication.
// The hash is computed from timestamp + namespace + pod + container + message.
// Null byte separators prevent collisions between different field combinations
// (e.g., namespace="a", pod="bc" vs namespace="ab", pod="c").
func computeDedupHash(timestampNano int64, namespace, pod, container, message string) int64 {
	h := fnv.New64a()

	// Write timestamp as 8 bytes (little-endian)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(timestampNano))
	h.Write(buf[:])

	// Write strings with null separators
	h.Write([]byte(namespace))
	h.Write([]byte{0})
	h.Write([]byte(pod))
	h.Write([]byte{0})
	h.Write([]byte(container))
	h.Write([]byte{0})
	h.Write([]byte(message))

	// Convert uint64 to int64 for SQLite INTEGER compatibility
	return int64(h.Sum64())
}
