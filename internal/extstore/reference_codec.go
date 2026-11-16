package extstore

import (
	"context"
	"errors"
	"fmt"
	"slices"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
)

// ReferenceCodec encodes and decodes storage reference payloads.
type ReferenceCodec interface {
	Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error)
	Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error)
}

// ReferenceCodecProvider returns the codec for the payloads being visited. The
// codec may depend on values carried in ctx, such as a serialization context.
type ReferenceCodecProvider func(ctx context.Context) ReferenceCodec

// HasReferenceMarker reports whether p carries the storage reference marker.
func HasReferenceMarker(p *commonpb.Payload) bool {
	return string(p.GetMetadata()[metadataReferenceMarker]) == "true"
}

// isHeaderPayload reports whether the visited payloads are header fields. Header
// codecs run per header outside the payload visitors, so storage references in
// headers are neither encoded nor decoded here.
func isHeaderPayload(ctx *proxy.VisitPayloadsContext) bool {
	_, ok := ctx.Parent.(*commonpb.Header)
	return ok
}

type referenceEncodingVisitor struct {
	codecFor ReferenceCodecProvider
}

// NewReferenceEncodingVisitor returns a visitor that encodes storage reference
// payloads with the provided codec. It must run after the storage visitor.
func NewReferenceEncodingVisitor(codecFor ReferenceCodecProvider) PayloadVisitor {
	return &referenceEncodingVisitor{codecFor: codecFor}
}

func (v *referenceEncodingVisitor) Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if isHeaderPayload(ctx) {
		return payloads, nil
	}
	var indices []int
	var references []*commonpb.Payload
	for i, p := range payloads {
		if IsStorageReference(p) {
			indices = append(indices, i)
			references = append(references, p)
		}
	}
	if len(references) == 0 {
		return payloads, nil
	}

	encoded, err := applyToEach(references, v.codecFor(ctx.Context).Encode)
	if err != nil {
		return nil, err
	}

	result := slices.Clone(payloads)
	for j, i := range indices {
		e := encoded[j]
		// Codecs usually build a new payload, dropping the marker and the size
		// details the server reads from ExternalPayloads.
		if e.Metadata == nil {
			e.Metadata = map[string][]byte{}
		}
		e.Metadata[metadataReferenceMarker] = []byte("true")
		if len(e.ExternalPayloads) == 0 {
			e.ExternalPayloads = references[j].ExternalPayloads
		}
		result[i] = e
	}
	return result, nil
}

type referenceDecodingVisitor struct {
	codecFor           ReferenceCodecProvider
	failWithoutStorage bool
}

// NewReferenceDecodingVisitor returns a visitor that decodes marked storage
// reference payloads with the provided codec. It must run before the retrieval
// visitor.
func NewReferenceDecodingVisitor(params StorageParameters, codecFor ReferenceCodecProvider) PayloadVisitor {
	return &referenceDecodingVisitor{codecFor: codecFor, failWithoutStorage: len(params.driverMap) == 0}
}

// NewReferenceOnlyDecodingVisitor returns a visitor that decodes marked storage
// reference payloads without requiring storage to be configured, for callers
// that return the references instead of retrieving them.
func NewReferenceOnlyDecodingVisitor(codecFor ReferenceCodecProvider) PayloadVisitor {
	return &referenceDecodingVisitor{codecFor: codecFor}
}

func (v *referenceDecodingVisitor) Visit(ctx *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if isHeaderPayload(ctx) {
		return payloads, nil
	}
	var indices []int
	var encoded []*commonpb.Payload
	for i, p := range payloads {
		if HasReferenceMarker(p) {
			indices = append(indices, i)
			encoded = append(encoded, p)
		}
	}
	if len(encoded) == 0 {
		return payloads, nil
	}
	if v.failWithoutStorage {
		return nil, errStorageNotConfigured
	}

	// The marker and ExternalPayloads stay on the payload the codec decodes. A
	// codec must tolerate them, because codec servers receive payloads exactly as
	// they appear in history.
	decoded, err := applyToEach(encoded, v.codecFor(ctx.Context).Decode)
	if err != nil {
		return nil, err
	}

	result := slices.Clone(payloads)
	for j, i := range indices {
		d := decoded[j]
		if !IsStorageReference(d) {
			return nil, errors.New("[TMPRL1106] Encountered an encoded external storage reference that could not be decoded. " +
				"Check that the payload codec used to write it is configured")
		}
		if len(d.ExternalPayloads) == 0 {
			d.ExternalPayloads = payloads[i].ExternalPayloads
		}
		result[i] = d
	}
	return result, nil
}

// applyToEach runs apply on each payload separately, one at a time. A codec may
// return fewer payloads than it is given, so results from a batch could not be
// matched to the payloads that produced them.
func applyToEach(payloads []*commonpb.Payload, apply func([]*commonpb.Payload) ([]*commonpb.Payload, error)) ([]*commonpb.Payload, error) {
	results := make([]*commonpb.Payload, len(payloads))
	for i, p := range payloads {
		out, err := apply([]*commonpb.Payload{p})
		if err != nil {
			return nil, err
		}
		if len(out) != 1 {
			return nil, fmt.Errorf("received %d payloads from codec, expected 1", len(out))
		}
		results[i] = out[0]
	}
	return results, nil
}
