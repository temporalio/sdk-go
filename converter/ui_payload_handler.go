// Package converter provides an HTTP handler for a Temporal codec server that
// understands the two-layer codec architecture used with external payload storage.
//
// The handler exposes two routes:
//
//   - POST /ui/decode — decodes payloads through the post-storage then pre-storage
//     codec chains. If a payload is a storage reference after post-storage
//     decoding it is returned as-is so the caller can resolve it via /ui/download.
//
//   - POST /ui/download — accepts storage reference payloads, retrieves the
//     original payloads from the configured storage drivers, then decodes
//     them through the pre-storage codec chain.
//
// The wire format for both routes is identical to the existing
// [NewPayloadCodecHTTPHandler]: a JSON-encoded [commonpb.Payloads] request body
// and response body, with Content-Type application/json.

package converter

import (
	"encoding/json"
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
	uiDecodePath   = "/ui/decode"
	uiDownloadPath = "/ui/download"
)

// UIPayloadHTTPHandlerOptions configures a storage and codec aware HTTP handler
// for use with Temporal Web UI.
//
// NOTE: Experimental
type UIPayloadHTTPHandlerOptions struct {
	// PostStorageCodecs are codecs applied outside the storage layer, e.g. a
	// proxy codec that wraps the entire payload.
	// They run last on encode (after external storage) and first on decode
	// (before external retrieval).
	//
	// NOTE: Experimental.
	PostStorageCodecs []PayloadCodec

	// PreStorageCodecs are worker-configured codecs that run before payloads
	// enter external storage, e.g. encryption or compression. They run first
	// on encode (before external storage) and last on decode (after external
	// retrieval).
	//
	// NOTE: Experimental.
	PreStorageCodecs []PayloadCodec

	// StorageDrivers provides the storage drivers used by the /download route to
	// retrieve payloads identified by storage reference claims. Driver names
	// must be unique. If no drivers are configured, /download returns HTTP 400.
	//
	// NOTE: Experimental.
	StorageDrivers []StorageDriver
}

// noopDriverSelector to satisfy the StorageDriverSelector interface when constructing
// the retrieval visitor. It is never actually used because the visitor only performs retrieval operations.
type noopDriverSelector struct{}

func (noopDriverSelector) SelectDriver(_ StorageDriverStoreContext, _ *commonpb.Payload) (StorageDriver, error) {
	return nil, nil
}

type uiPayloadHTTPHandler struct {
	postStorageCodecs []PayloadCodec
	preStorageCodecs  []PayloadCodec
	retrievalVisitor  extstore.PayloadVisitor // nil when no drivers configured
}

// NewUIPayloadHTTPHandler creates an [http.Handler] that serves /ui/decode and
// /ui/download routes for Temporal Web UI using the provided options.
//
// NOTE: Experimental
func NewUIPayloadHTTPHandler(options UIPayloadHTTPHandlerOptions) (http.Handler, error) {
	h := &uiPayloadHTTPHandler{
		postStorageCodecs: options.PostStorageCodecs,
		preStorageCodecs:  options.PreStorageCodecs,
	}
	if len(options.StorageDrivers) > 0 {
		params, err := extstore.ExternalStorageToParams(ExternalStorage{
			Drivers:        options.StorageDrivers,
			DriverSelector: noopDriverSelector{},
		})
		if err != nil {
			return nil, err
		}
		h.retrievalVisitor = extstore.NewExternalRetrievalVisitor(params)
	}
	return h, nil
}

// ServeHTTP implements [http.Handler].
func (h *uiPayloadHTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	path := r.URL.Path
	if !strings.HasSuffix(path, uiDecodePath) &&
		!strings.HasSuffix(path, uiDownloadPath) {
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
	case strings.HasSuffix(path, uiDecodePath):
		payloads, err = h.decode(payloads)
	case strings.HasSuffix(path, uiDownloadPath):
		payloads, err = h.download(r, payloads)
	default:
		http.NotFound(w, r)
		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err = json.NewEncoder(w).Encode(commonpb.Payloads{Payloads: payloads}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}

// decode applies post-storage codecs (first-to-last) to all payloads. Any
// payload that is a storage reference after this step is returned as-is so
// the caller can resolve it via /ui/download. Remaining payloads are further
// decoded through the pre-storage codecs (first-to-last).
func (h *uiPayloadHTTPHandler) decode(payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	var err error

	// Apply post-storage codecs to all payloads.
	for _, c := range h.postStorageCodecs {
		if payloads, err = c.Decode(payloads); err != nil {
			return nil, err
		}
	}

	// Separate storage references — they cannot be pre-storage decoded yet.
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

	// Apply pre-storage codecs to non-reference payloads.
	for _, c := range h.preStorageCodecs {
		if nonRefPayloads, err = c.Decode(nonRefPayloads); err != nil {
			return nil, err
		}
	}
	for j, i := range nonRefIdxs {
		result[i] = nonRefPayloads[j]
	}
	return result, nil
}

// download validates that every payload is a storage reference, retrieves the
// original payloads from the registered storage drivers, then decodes them
// through the pre-storage codec chain.
func (h *uiPayloadHTTPHandler) download(r *http.Request, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	if h.retrievalVisitor == nil {
		return nil, errors.New("no storage drivers configured")
	}
	for _, p := range payloads {
		if !extstore.IsStorageReference(p) {
			return nil, errors.New("all payloads must be storage references")
		}
	}

	vpc := &proxy.VisitPayloadsContext{Context: r.Context()}
	retrieved, err := h.retrievalVisitor.Visit(vpc, payloads)
	if err != nil {
		return nil, err
	}

	for _, c := range h.preStorageCodecs {
		if retrieved, err = c.Decode(retrieved); err != nil {
			return nil, err
		}
	}
	return retrieved, nil
}
