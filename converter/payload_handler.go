// Package converter provides an HTTP handler for a Temporal codec server with
// support for external payload storage.

package converter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/internal/extstore"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	downloadPath = "/download"
)

// PayloadHTTPHandlerOptions configures a [NewPayloadHTTPHandler].
//
// NOTE: Experimental
type PayloadHTTPHandlerOptions struct {
	// Codecs are the codecs configured on the client's DataConverter. They are
	// applied to payloads before external storage on encode, and to encoded
	// storage references and retrieved payloads on decode. The codecs are applied
	// last to first meaning the earlier codecs wrap the later ones. When decoding,
	// the codecs are applied first to last to reverse the effect.
	//
	// NOTE: Experimental.
	Codecs []PayloadCodec

	// ExternalStorage configures external payload storage, allowing payloads
	// to be stored and retrieved from external sources if they meet the size
	// threshold and driver selection criteria.
	//
	// NOTE: Experimental.
	ExternalStorage ExternalStorage
}

type payloadHTTPHandler struct {
	codecs                       []PayloadCodec
	referenceDecodingVisitor     extstore.PayloadVisitor
	referenceOnlyDecodingVisitor extstore.PayloadVisitor
	referenceEncodingVisitor     extstore.PayloadVisitor
	retrievalVisitor             extstore.PayloadVisitor
	storageVisitor               extstore.PayloadVisitor
}

var _ http.Handler = (*payloadHTTPHandler)(nil)

// NewPayloadHTTPHandler creates an [http.Handler] that serves /encode, /decode,
// and /download routes for remote payload transformations.
//
// NOTE: Experimental
func NewPayloadHTTPHandler(options PayloadHTTPHandlerOptions) (http.Handler, error) {
	params, err := extstore.ExternalStorageToParams(options.ExternalStorage)
	if err != nil {
		return nil, err
	}

	referenceCodec := func(context.Context) extstore.ReferenceCodec { return codecChain(options.Codecs) }
	h := &payloadHTTPHandler{
		codecs:                       options.Codecs,
		referenceDecodingVisitor:     extstore.NewReferenceDecodingVisitor(params, referenceCodec),
		referenceOnlyDecodingVisitor: extstore.NewReferenceOnlyDecodingVisitor(referenceCodec),
		referenceEncodingVisitor:     extstore.NewReferenceEncodingVisitor(referenceCodec),
		retrievalVisitor:             extstore.NewExternalRetrievalVisitor(params),
		storageVisitor:               extstore.NewExternalStorageVisitor(params),
	}
	return h, nil
}

// ServeHTTP implements [http.Handler].
func (h *payloadHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	path := r.URL.Path
	if !strings.HasSuffix(path, remotePayloadCodecDecodePath) &&
		!strings.HasSuffix(path, remotePayloadCodecEncodePath) &&
		!strings.HasSuffix(path, downloadPath) {
		http.NotFound(w, r)
		return
	}

	if r.Body == nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	bs, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var payloadspb commonpb.Payloads
	if err = protojson.Unmarshal(bs, &payloadspb); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	payloads := payloadspb.Payloads

	switch {
	case strings.HasSuffix(path, remotePayloadCodecDecodePath):
		payloads, err = h.decode(r, payloads)
	case strings.HasSuffix(path, remotePayloadCodecEncodePath):
		payloads, err = h.encode(r, payloads)
	case strings.HasSuffix(path, downloadPath):
		payloads, err = h.download(r, payloads)
	default:
		http.NotFound(w, r)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	bs, err = protojson.Marshal(&commonpb.Payloads{Payloads: payloads})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err = w.Write(bs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// decode decodes storage references, retrieves the payloads they refer to, and
// decodes the result. If preserveStorageRefs=true is set in the query string,
// storage references are returned decoded but not retrieved.
func (h *payloadHTTPHandler) decode(r *http.Request, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	vpc := &proxy.VisitPayloadsContext{Context: r.Context()}
	var err error
	if strings.EqualFold(r.URL.Query().Get("preserveStorageRefs"), "true") {
		payloads, err = h.referenceOnlyDecodingVisitor.Visit(vpc, payloads)
	} else {
		payloads, err = h.referenceDecodingVisitor.Visit(vpc, payloads)
		if err == nil {
			payloads, err = h.retrievalVisitor.Visit(vpc, payloads)
		}
	}
	if err != nil {
		return nil, err
	}

	return decodeNonReferences(payloads, h.codecs)
}

// download retrieves payloads from external storage and decodes them. All input
// payloads must be storage references.
func (h *payloadHTTPHandler) download(r *http.Request, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	for _, p := range payloads {
		if !extstore.HasReferenceMarker(p) && !extstore.IsStorageReference(p) {
			return nil, errors.New("all payloads must be storage references")
		}
	}

	vpc := &proxy.VisitPayloadsContext{Context: r.Context()}
	references, err := h.referenceDecodingVisitor.Visit(vpc, payloads)
	if err != nil {
		return nil, err
	}
	retrieved, err := h.retrievalVisitor.Visit(vpc, references)
	if err != nil {
		return nil, err
	}

	return decodeNonReferences(retrieved, h.codecs)
}

// encode encodes payloads, applying external storage as configured, and encodes
// the storage references that replace stored payloads.
func (h *payloadHTTPHandler) encode(r *http.Request, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	payloads, err := encodePayloads(payloads, h.codecs)
	if err != nil {
		return nil, err
	}

	vpc := &proxy.VisitPayloadsContext{Context: r.Context()}
	payloads, err = h.storageVisitor.Visit(vpc, payloads)
	if err != nil {
		return nil, err
	}
	if !extstore.WriteEncodedReferences {
		return payloads, nil
	}

	return h.referenceEncodingVisitor.Visit(vpc, payloads)
}

// codecChain applies codecs the same way a CodecDataConverter does.
type codecChain []PayloadCodec

func (c codecChain) Encode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return encodePayloads(payloads, c)
}

func (c codecChain) Decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	return decodePayloads(payloads, c)
}

// decodeNonReferences decodes non-storage-reference payloads through the given
// codec chain. Storage references pass through as-is.
func decodeNonReferences(payloads []*commonpb.Payload, codecs []PayloadCodec) ([]*commonpb.Payload, error) {
	result := make([]*commonpb.Payload, len(payloads))
	var nonRefIdxs []int
	var nonRefPayloads []*commonpb.Payload
	for i, p := range payloads {
		if extstore.IsStorageReference(p) {
			result[i] = p
		} else {
			nonRefIdxs = append(nonRefIdxs, i)
			nonRefPayloads = append(nonRefPayloads, p)
		}
	}
	decoded, err := decodePayloads(nonRefPayloads, codecs)
	if err != nil {
		return nil, err
	}
	for j, idx := range nonRefIdxs {
		result[idx] = decoded[j]
	}
	return result, nil
}
