// Package store holds the filesystem implementations of the engine's
// persistence ports: the verdict cache, the acceptance list and the
// characterisation provenance registry.
//
// The engine owns the policies that use these documents — which key a
// verdict is stored under, which entry may be replayed, which population may
// write the accepted list, which ledger entry permits a replacement — and
// keeps every key dimension and format version. This package owns the
// documents themselves: where they live, their wire shapes, their modes,
// their atomic installation, and the rule that every irregularity on read is
// the absence the caller can recover from.
package store
