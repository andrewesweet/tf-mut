package engine

import (
	"github.com/andrewesweet/tf-mut/internal/discovery"
	"github.com/andrewesweet/tf-mut/internal/fingerprint"
)

// staticMask converts the static impure scan into fingerprint mask entries.
//
// The scan reaches only top-level resource arguments, because those are the
// paths whose position in the payload is decidable without the attribute-level
// provenance graph M3 supplies: `input` on `terraform_data.app` is
// `change.after.input`, and nothing deeper is derivable from the syntax alone.
// Everything the scan cannot place is left to the two-run baseline diff, which
// observes rather than infers.
//
// ponytail: top-level attributes only; nested paths need M3's provenance.
func staticMask(scan discovery.VolatilityScan, payloads []fingerprint.Payload) fingerprint.Mask {
	mask := fingerprint.NewMask()

	for _, volatile := range scan.Attributes {
		span := fingerprint.SyntaxSpan(volatile.Prefix, volatile.Suffix, volatile.Whole)

		for _, path := range payloadPaths(payloads, volatile.Resource, volatile.Attribute) {
			mask.Spans[path] = span
		}
	}

	return mask
}

// payloadPaths lists the canonical paths a resource argument occupies in the
// payloads of a run set.
func payloadPaths(payloads []fingerprint.Payload, address, attribute string) []string {
	found := []string{}

	for _, payload := range payloads {
		for path := range payload.Values {
			if fingerprint.AttributePath(path, address, attribute) {
				found = append(found, path)
			}
		}
	}

	return found
}
