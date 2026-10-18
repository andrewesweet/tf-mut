// Package store holds the filesystem implementations of the engine's
// persistence ports: the verdict cache, the acceptance list and the
// characterisation provenance registry.
//
// The engine owns the policies that use these documents — which key a
// verdict is stored under, which entry may be replayed, which population may
// write the accepted list, which ledger entry permits a replacement — and
// keeps every key dimension, document location and cache format version.
// This package owns the wire shapes, baseline and registry format versions,
// filesystem permissions and write protocols. Cache irregularities are
// misses; unreadable registries claim nothing. A missing baseline is empty,
// but an unreadable, corrupt or foreign-format baseline is an error.
package store
