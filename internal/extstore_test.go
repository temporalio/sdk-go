package internal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/converter"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------------------------
// testStorageDriver — in-memory StorageDriver for testing
// ---------------------------------------------------------------------------

type testStorageDriver struct {
	name          string
	mu            sync.Mutex
	data          map[string]*commonpb.Payload
	storeCount    int
	retrieveCount int
	storeErr      error
	retrieveErr   error
	storeDelay    time.Duration
	retrieveDelay time.Duration
}

func newTestDriver(name string) *testStorageDriver {
	return &testStorageDriver{name: name, data: map[string]*commonpb.Payload{}}
}

func (d *testStorageDriver) Name() string { return d.name }
func (d *testStorageDriver) Type() string { return "test" }

func (d *testStorageDriver) Store(_ converter.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]converter.StorageClaim, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.storeCount++
	if d.storeDelay > 0 {
		time.Sleep(d.storeDelay)
	}
	if d.storeErr != nil {
		return nil, d.storeErr
	}
	claims := make([]converter.StorageClaim, len(payloads))
	for i, p := range payloads {
		key := uuid.NewString()
		d.data[key] = proto.Clone(p).(*commonpb.Payload)
		claims[i] = converter.StorageClaim{ClaimData: map[string]string{"key": key}}
	}
	return claims, nil
}

func (d *testStorageDriver) Retrieve(_ converter.StorageDriverRetrieveContext, claims []converter.StorageClaim) ([]*commonpb.Payload, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retrieveCount++
	if d.retrieveDelay > 0 {
		time.Sleep(d.retrieveDelay)
	}
	if d.retrieveErr != nil {
		return nil, d.retrieveErr
	}
	payloads := make([]*commonpb.Payload, len(claims))
	for i, c := range claims {
		p, ok := d.data[c.ClaimData["key"]]
		if !ok {
			return nil, fmt.Errorf("key not found: %q", c.ClaimData["key"])
		}
		payloads[i] = proto.Clone(p).(*commonpb.Payload)
	}
	return payloads, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makePayload(t *testing.T, data string) *commonpb.Payload {
	t.Helper()
	p, err := converter.GetDefaultDataConverter().ToPayload(data)
	require.NoError(t, err)
	return p
}

// makeOversizedPayload returns a payload whose proto.Size() is >= threshold.
func makeOversizedPayload(t *testing.T, threshold int) *commonpb.Payload {
	t.Helper()
	data := make([]byte, threshold)
	for i := range data {
		data[i] = 'x'
	}
	return &commonpb.Payload{Data: data}
}

func visitPayloads(ctx context.Context, visitor PayloadVisitor, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
	vpc := &proxy.VisitPayloadsContext{Context: ctx}
	return visitor.Visit(vpc, payloads)
}

// ---------------------------------------------------------------------------
// ExternalStorageToParams
// ---------------------------------------------------------------------------

func TestExternalStorageToParams_NegativeThreshold(t *testing.T) {
	_, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{newTestDriver("d")},
		PayloadSizeThreshold: -1,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "PayloadSizeThreshold")
}

func TestExternalStorageToParams_ZeroThresholdUsesDefault(t *testing.T) {
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{newTestDriver("d")},
	})
	require.NoError(t, err)
	require.Equal(t, defaultPayloadSizeThreshold, params.payloadSizeThreshold)
}

func TestExternalStorageToParams_ExplicitThreshold(t *testing.T) {
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{newTestDriver("d")},
		PayloadSizeThreshold: 1024,
	})
	require.NoError(t, err)
	require.Equal(t, 1024, params.payloadSizeThreshold)
}

func TestExternalStorageToParams_MultipleDriversRequireSelector(t *testing.T) {
	_, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{newTestDriver("a"), newTestDriver("b")},
	})
	require.EqualError(t, err, "DriverSelector must be set when more than one driver is provided")
}

func TestExternalStorageToParams_DuplicateDriverNames(t *testing.T) {
	_, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{newTestDriver("same"), newTestDriver("same")},
	})
	require.EqualError(t, err, `duplicate storage driver name: "same"`)
}

func TestExternalStorageToParams_PointerAndValueReceiverDrivers(t *testing.T) {
	// ptrDriver satisfies StorageDriver via pointer receivers (*testStorageDriver).
	ptrDriver := newTestDriver("ptr-driver")
	// valDriver satisfies StorageDriver via value receivers (valueReceiverDriver).
	// Both the plain value and a pointer to it implement the interface; here we
	// register the plain value to confirm that path works too.
	//valDriver := valueReceiverDriver{name: "val-driver"}
	valDriver := newValDriver("val-driver")

	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		return ptrDriver, nil
	}}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{ptrDriver, valDriver},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1, // store everything
	})
	require.NoError(t, err)
	require.True(t, driversEqual(params.driverMap["ptr-driver"], ptrDriver), "pointer receiver driver should be in the registered set")
	require.True(t, driversEqual(params.driverMap["val-driver"], valDriver), "value receiver driver should be in the registered set")
}

func TestExternalStorageToParams_EmptyDrivers(t *testing.T) {
	params, err := ExternalStorageToParams(converter.ExternalStorage{})
	require.NoError(t, err)
	require.Nil(t, params.driverSelector)
}

func TestExternalStorageToParams_SingleDriverSynthesizesSelector(t *testing.T) {
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{driver},
	})
	require.NoError(t, err)
	require.NotNil(t, params.driverSelector)
	selected, err := params.driverSelector.SelectDriver(converter.StorageDriverStoreContext{Context: context.Background()}, nil)
	require.NoError(t, err)
	require.Equal(t, driver, selected)
}

// ---------------------------------------------------------------------------
// storageReferenceToPayload / payloadToStorageReference
// ---------------------------------------------------------------------------

func TestStorageReferenceRoundTrip(t *testing.T) {
	ref := storageReference{
		DriverName:  "mydriver",
		DriverClaim: converter.StorageClaim{ClaimData: map[string]string{"key": "abc123"}},
	}
	p, err := storageReferenceToPayload(ref, 512)
	require.NoError(t, err)
	require.Equal(t, metadataEncodingStorageRef, string(p.Metadata[converter.MetadataEncoding]))
	require.Len(t, p.ExternalPayloads, 1)
	require.Equal(t, int64(512), p.ExternalPayloads[0].SizeBytes)

	decoded, err := payloadToStorageReference(p)
	require.NoError(t, err)
	require.Equal(t, ref.DriverName, decoded.DriverName)
	require.Equal(t, ref.DriverClaim.ClaimData, decoded.DriverClaim.ClaimData)
}

func TestPayloadToStorageReference_WrongEncoding(t *testing.T) {
	p := &commonpb.Payload{
		Metadata: map[string][]byte{converter.MetadataEncoding: []byte("json/plain")},
		Data:     []byte(`{}`),
	}
	_, err := payloadToStorageReference(p)
	require.Error(t, err)
}

func TestPayloadToStorageReference_CorruptJSON(t *testing.T) {
	p := &commonpb.Payload{
		Metadata: map[string][]byte{converter.MetadataEncoding: []byte(metadataEncodingStorageRef)},
		Data:     []byte(`not json`),
	}
	_, err := payloadToStorageReference(p)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// storageStoreVisitor
// ---------------------------------------------------------------------------

func TestStoreVisitor_NoDriverNoop(t *testing.T) {
	params, err := ExternalStorageToParams(converter.ExternalStorage{})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	p := makePayload(t, "hello")
	result, err := visitPayloads(context.Background(), visitor, []*commonpb.Payload{p})
	require.NoError(t, err)
	require.True(t, proto.Equal(p, result[0]))
}

func TestStoreVisitor_BelowThreshold_NotStored(t *testing.T) {
	driver := newTestDriver("d")
	// Use a large threshold so the payload stays inline.
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1 << 20, // 1 MiB
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	p := makePayload(t, "small")
	result, err := visitPayloads(context.Background(), visitor, []*commonpb.Payload{p})
	require.NoError(t, err)
	require.True(t, proto.Equal(p, result[0]), "small payload should be unchanged")
	require.Equal(t, 0, driver.storeCount)
}

func TestStoreVisitor_AtThreshold_Stored(t *testing.T) {
	const threshold = 100
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: threshold,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	p := makeOversizedPayload(t, threshold)
	require.GreaterOrEqual(t, proto.Size(p), threshold)

	result, err := visitPayloads(context.Background(), visitor, []*commonpb.Payload{p})
	require.NoError(t, err)
	require.Equal(t, metadataEncodingStorageRef, string(result[0].Metadata[converter.MetadataEncoding]))
	require.Equal(t, 1, driver.storeCount)
}

func TestStoreVisitor_AboveThreshold_Stored(t *testing.T) {
	const threshold = 10
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: threshold,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	p := makeOversizedPayload(t, threshold+1)
	result, err := visitPayloads(context.Background(), visitor, []*commonpb.Payload{p})
	require.NoError(t, err)
	require.Equal(t, metadataEncodingStorageRef, string(result[0].Metadata[converter.MetadataEncoding]))
	require.Equal(t, 1, driver.storeCount)
}

func TestStoreVisitor_MultiplePayloads_Batched(t *testing.T) {
	const threshold = 500
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: threshold,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	big1 := makeOversizedPayload(t, threshold)
	small := &commonpb.Payload{Data: []byte("x")} // proto.Size ≈ 3, well below threshold
	big2 := makeOversizedPayload(t, threshold)

	result, err := visitPayloads(context.Background(), visitor, []*commonpb.Payload{big1, small, big2})
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, metadataEncodingStorageRef, string(result[0].Metadata[converter.MetadataEncoding]))
	require.Equal(t, metadataEncodingStorageRef, string(result[2].Metadata[converter.MetadataEncoding]))
	// small payload is inline
	require.Empty(t, result[1].ExternalPayloads)
	// both big payloads batched in a single Store call
	require.Equal(t, 1, driver.storeCount)
}

func TestStoreVisitor_SelectorNil_PayloadInline(t *testing.T) {
	driver := newTestDriver("d")
	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		return nil, nil
	}}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:        []converter.StorageDriver{driver},
		DriverSelector: selector,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	p := makeOversizedPayload(t, defaultPayloadSizeThreshold+1)
	result, err := visitPayloads(context.Background(), visitor, []*commonpb.Payload{p})
	require.NoError(t, err)
	require.Empty(t, result[0].ExternalPayloads, "selector returned nil so payload should be inline")
	require.Equal(t, 0, driver.storeCount)
}

func TestStoreVisitor_SelectorBelowThreshold_NotCalled(t *testing.T) {
	driver := newTestDriver("d")
	selectorCalled := false
	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		selectorCalled = true
		return driver, nil
	}}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1 << 20, // 1 MiB
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	p := makePayload(t, "small")
	result, err := visitPayloads(context.Background(), visitor, []*commonpb.Payload{p})
	require.NoError(t, err)
	require.False(t, selectorCalled, "selector must not be invoked for sub-threshold payloads")
	require.Empty(t, result[0].ExternalPayloads)
	require.Equal(t, 0, driver.storeCount)
}

func TestStoreVisitor_SelectorRoutes_TwoDrivers(t *testing.T) {
	d1 := newTestDriver("d1")
	d2 := newTestDriver("d2")
	i := 0
	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		defer func() { i++ }()
		if i%2 == 0 {
			return d1, nil
		}
		return d2, nil
	}}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{d1, d2},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	p1 := makePayload(t, "a")
	p2 := makePayload(t, "b")
	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{p1, p2})
	require.NoError(t, err)
	require.Equal(t, 1, d1.storeCount)
	require.Equal(t, 1, d2.storeCount)
}

func TestStoreVisitor_SelectorUnregisteredDriver(t *testing.T) {
	unregistered := newTestDriver("my-unregistered-driver")
	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		return unregistered, nil
	}}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{newTestDriver("registered")},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{makePayload(t, "x")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "my-unregistered-driver")
}

func TestStoreVisitor_SelectorError(t *testing.T) {
	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		return nil, errors.New("selector error")
	}}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{newTestDriver("d")},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{makePayload(t, "x")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "selector error")
}

func TestStoreVisitor_WrongClaimCount(t *testing.T) {
	driver := &badCountDriver{name: "my-bad-count-driver"}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{makePayload(t, "x")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "my-bad-count-driver")
}

func TestStoreVisitor_StoreError(t *testing.T) {
	driver := newTestDriver("my-bad-error-driver")
	driver.storeErr = errors.New("store failed")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{makePayload(t, "x")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "my-bad-error-driver")
	require.Contains(t, err.Error(), "store failed")
}

func TestStoreVisitor_StorePanic(t *testing.T) {
	driver := &panicDriver{name: "my-panic-store-driver", panicOnStore: true}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{makePayload(t, "x")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "my-panic-store-driver")
	require.Contains(t, err.Error(), "store panic")
}

func TestStoreVisitor_CancelOnError(t *testing.T) {
	errD := newTestDriver("err-driver")
	errD.storeErr = errors.New("store error")
	blockD := &blockingDriver{name: "block-driver", cancelledCh: make(chan struct{})}

	i := 0
	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		defer func() { i++ }()
		if i%2 == 0 {
			return errD, nil
		}
		return blockD, nil
	}}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{errD, blockD},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	p1, p2 := makePayload(t, "a"), makePayload(t, "b")
	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{p1, p2})
	require.Error(t, err)
	require.Contains(t, err.Error(), "store error")

	select {
	case <-blockD.cancelledCh:
	default:
		t.Fatal("blocking driver context was not cancelled after sibling driver error")
	}
}

func TestStoreVisitor_Callback(t *testing.T) {
	driver := newTestDriver("d")
	driver.storeDelay = time.Millisecond
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	cb := &testCallback{}
	ctx := context.WithValue(context.Background(), storageOperationCallbackContextKey, cb)
	_, err = visitPayloads(ctx, visitor, []*commonpb.Payload{makePayload(t, "x"), makePayload(t, "y")})
	require.NoError(t, err)
	require.Equal(t, 2, cb.count)
	require.Greater(t, cb.size, int64(0))
	require.Greater(t, cb.duration, time.Duration(0))
}

func TestStoreVisitor_Callback_ExternalCountOnly(t *testing.T) {
	const threshold = 50
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: threshold,
	})
	require.NoError(t, err)
	visitor := NewExternalStorageVisitor(params)

	small := makePayload(t, "x")
	big1 := makeOversizedPayload(t, threshold)
	big2 := makeOversizedPayload(t, threshold)

	cb := &testCallback{}
	ctx := context.WithValue(context.Background(), storageOperationCallbackContextKey, cb)
	_, err = visitPayloads(ctx, visitor, []*commonpb.Payload{small, big1, big2})
	require.NoError(t, err)
	require.Equal(t, 2, cb.count)
}

// ---------------------------------------------------------------------------
// storageRetrievalVisitor
// ---------------------------------------------------------------------------

func TestRetrievalVisitor_InlinePassthrough(t *testing.T) {
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{driver},
	})
	require.NoError(t, err)
	visitor := NewExternalRetrievalVisitor(params)

	p := makePayload(t, "inline")
	result, err := visitPayloads(context.Background(), visitor, []*commonpb.Payload{p})
	require.NoError(t, err)
	require.True(t, proto.Equal(p, result[0]))
	require.Equal(t, 0, driver.retrieveCount)
}

func TestRetrievalVisitor_Mixed(t *testing.T) {
	driver := newTestDriver("d")
	storeParams, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(storeParams)

	inline := makePayload(t, "inline")
	big := makeOversizedPayload(t, 100)

	stored, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{big})
	require.NoError(t, err)
	ref := stored[0]

	retrieveVisitor := NewExternalRetrievalVisitor(storeParams)
	result, err := visitPayloads(context.Background(), retrieveVisitor, []*commonpb.Payload{inline, ref})
	require.NoError(t, err)
	require.True(t, proto.Equal(inline, result[0]))
	require.True(t, proto.Equal(big, result[1]))
}

func TestRetrievalVisitor_BatchedByDriver(t *testing.T) {
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(params)
	retrieveVisitor := NewExternalRetrievalVisitor(params)

	p1, p2 := makePayload(t, "first"), makePayload(t, "second")
	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{p1, p2})
	require.NoError(t, err)

	driver.retrieveCount = 0
	result, err := visitPayloads(context.Background(), retrieveVisitor, refs)
	require.NoError(t, err)
	require.Equal(t, 1, driver.retrieveCount, "both claims should be batched into one Retrieve call")
	require.True(t, proto.Equal(p1, result[0]))
	require.True(t, proto.Equal(p2, result[1]))
}

func TestRetrievalVisitor_MultiDriver(t *testing.T) {
	d1 := newTestDriver("d1")
	d2 := newTestDriver("d2")
	i := 0
	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		defer func() { i++ }()
		if i == 0 {
			return d1, nil
		}
		return d2, nil
	}}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{d1, d2},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(params)
	retrieveVisitor := NewExternalRetrievalVisitor(params)

	p1, p2 := makePayload(t, "a"), makePayload(t, "b")
	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{p1, p2})
	require.NoError(t, err)

	result, err := visitPayloads(context.Background(), retrieveVisitor, refs)
	require.NoError(t, err)
	require.True(t, proto.Equal(p1, result[0]))
	require.True(t, proto.Equal(p2, result[1]))
	require.Equal(t, 1, d1.retrieveCount)
	require.Equal(t, 1, d2.retrieveCount)
}

func TestRetrievalVisitor_UnknownDriver(t *testing.T) {
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{newTestDriver("registered")},
	})
	require.NoError(t, err)
	visitor := NewExternalRetrievalVisitor(params)

	ref, err := storageReferenceToPayload(storageReference{
		DriverName:  "unregistered-driver",
		DriverClaim: converter.StorageClaim{ClaimData: map[string]string{"key": "k"}},
	}, 10)
	require.NoError(t, err)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{ref})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unregistered-driver")
}

func TestRetrievalVisitor_RetrieveError(t *testing.T) {
	driver := newTestDriver("my-bad-error-driver")
	driver.retrieveErr = errors.New("retrieve error")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(params)
	retrieveVisitor := NewExternalRetrievalVisitor(params)

	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{makePayload(t, "x")})
	require.NoError(t, err)

	_, err = visitPayloads(context.Background(), retrieveVisitor, refs)
	require.Error(t, err)
	require.Contains(t, err.Error(), "my-bad-error-driver")
	require.Contains(t, err.Error(), "retrieve error")
}

func TestRetrievalVisitor_RetrievePanic(t *testing.T) {
	driver := &panicDriver{name: "my-panic-retrieve-driver", panicOnRetrieve: true}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{driver},
	})
	require.NoError(t, err)
	visitor := NewExternalRetrievalVisitor(params)

	ref, err := storageReferenceToPayload(storageReference{
		DriverName:  "my-panic-retrieve-driver",
		DriverClaim: converter.StorageClaim{ClaimData: map[string]string{"key": "k"}},
	}, 10)
	require.NoError(t, err)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{ref})
	require.Error(t, err)
	require.Contains(t, err.Error(), "my-panic-retrieve-driver")
	require.Contains(t, err.Error(), "retrieve panic")
}

func TestRetrievalVisitor_CancelOnError(t *testing.T) {
	errD := newTestDriver("err-driver")
	errD.retrieveErr = errors.New("retrieve error")
	blockD := &blockingDriver{name: "block-driver", cancelledCh: make(chan struct{})}

	// Store via errD so we get a real reference for errD, then hand-craft a
	// reference for blockD.
	storeParams, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{errD},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(storeParams)
	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{makePayload(t, "x")})
	require.NoError(t, err)
	errRef := refs[0]

	blockRef, err := storageReferenceToPayload(storageReference{
		DriverName:  "block-driver",
		DriverClaim: converter.StorageClaim{ClaimData: map[string]string{"key": "k"}},
	}, 10)
	require.NoError(t, err)

	retrieveParams, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{errD, blockD},
	})
	require.NoError(t, err)
	visitor := NewExternalRetrievalVisitor(retrieveParams)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{errRef, blockRef})
	require.Error(t, err)
	require.Contains(t, err.Error(), "retrieve error")

	select {
	case <-blockD.cancelledCh:
	default:
		t.Fatal("blocking driver context was not cancelled after sibling driver error")
	}
}

func TestRetrievalVisitor_WrongPayloadCount(t *testing.T) {
	driver := &badCountRetrieveDriver{name: "my-bad-count-driver"}
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers: []converter.StorageDriver{driver},
	})
	require.NoError(t, err)
	visitor := NewExternalRetrievalVisitor(params)

	ref, err := storageReferenceToPayload(storageReference{
		DriverName:  "my-bad-count-driver",
		DriverClaim: converter.StorageClaim{ClaimData: map[string]string{"key": "k"}},
	}, 10)
	require.NoError(t, err)

	_, err = visitPayloads(context.Background(), visitor, []*commonpb.Payload{ref})
	require.Error(t, err)
	require.Contains(t, err.Error(), "my-bad-count-driver")
}

func TestRetrievalVisitor_Callback(t *testing.T) {
	driver := newTestDriver("d")
	driver.retrieveDelay = time.Millisecond
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(params)
	retrieveVisitor := NewExternalRetrievalVisitor(params)

	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{makePayload(t, "x")})
	require.NoError(t, err)

	cb := &testCallback{}
	ctx := context.WithValue(context.Background(), storageOperationCallbackContextKey, cb)
	_, err = visitPayloads(ctx, retrieveVisitor, refs)
	require.NoError(t, err)
	require.Greater(t, cb.duration, time.Duration(0))
}

func TestRetrievalVisitor_Callback_ExternalCountOnly(t *testing.T) {
	const threshold = 50
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: threshold,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(params)
	retrieveVisitor := NewExternalRetrievalVisitor(params)

	big1 := makeOversizedPayload(t, threshold)
	big2 := makeOversizedPayload(t, threshold)
	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{big1, big2})
	require.NoError(t, err)

	inline := makePayload(t, "inline")
	batch := []*commonpb.Payload{inline, refs[0], refs[1]}

	cb := &testCallback{}
	ctx := context.WithValue(context.Background(), storageOperationCallbackContextKey, cb)
	_, err = visitPayloads(ctx, retrieveVisitor, batch)
	require.NoError(t, err)
	require.Equal(t, 2, cb.count)
}

// ---------------------------------------------------------------------------
// Round-trip
// ---------------------------------------------------------------------------

func TestStoreRetrieveRoundTrip_Single(t *testing.T) {
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: 1,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(params)
	retrieveVisitor := NewExternalRetrievalVisitor(params)

	original := makePayload(t, "round-trip value")
	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{original})
	require.NoError(t, err)
	require.Equal(t, metadataEncodingStorageRef, string(refs[0].Metadata[converter.MetadataEncoding]))

	restored, err := visitPayloads(context.Background(), retrieveVisitor, refs)
	require.NoError(t, err)
	require.True(t, proto.Equal(original, restored[0]))
}

func TestStoreRetrieveRoundTrip_MixedInline(t *testing.T) {
	const threshold = 50
	driver := newTestDriver("d")
	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{driver},
		PayloadSizeThreshold: threshold,
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(params)
	retrieveVisitor := NewExternalRetrievalVisitor(params)

	small := makePayload(t, "s")
	big := makeOversizedPayload(t, threshold+1)

	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{small, big})
	require.NoError(t, err)

	restored, err := visitPayloads(context.Background(), retrieveVisitor, refs)
	require.NoError(t, err)
	require.True(t, proto.Equal(small, restored[0]), "inline payload unchanged")
	require.True(t, proto.Equal(big, restored[1]), "stored payload restored")
}

func TestStoreRetrieveRoundTrip_PointerAndValueReceiverDrivers(t *testing.T) {
	ptrDriver := newTestDriver("ptr-driver") // pointer receivers (*testStorageDriver)
	valDriver := newValDriver("val-driver")  // value receivers (valueReceiverDriver)

	// Route the first payload to ptrDriver and the second to valDriver.
	i := 0
	selector := &funcDriverSelector{fn: func(_ converter.StorageDriverStoreContext, _ *commonpb.Payload) (converter.StorageDriver, error) {
		defer func() { i++ }()
		if i == 0 {
			return ptrDriver, nil
		}
		return valDriver, nil
	}}

	params, err := ExternalStorageToParams(converter.ExternalStorage{
		Drivers:              []converter.StorageDriver{ptrDriver, valDriver},
		DriverSelector:       selector,
		PayloadSizeThreshold: 1, // store everything
	})
	require.NoError(t, err)
	storeVisitor := NewExternalStorageVisitor(params)
	retrieveVisitor := NewExternalRetrievalVisitor(params)

	p1 := makePayload(t, "via-ptr-driver")
	p2 := makePayload(t, "via-val-driver")

	refs, err := visitPayloads(context.Background(), storeVisitor, []*commonpb.Payload{p1, p2})
	require.NoError(t, err)

	// Confirm each payload was routed to the expected driver.
	ref0, err := payloadToStorageReference(refs[0])
	require.NoError(t, err)
	require.Equal(t, "ptr-driver", ref0.DriverName)

	ref1, err := payloadToStorageReference(refs[1])
	require.NoError(t, err)
	require.Equal(t, "val-driver", ref1.DriverName)

	// Both payloads should round-trip back to their original values.
	result, err := visitPayloads(context.Background(), retrieveVisitor, refs)
	require.NoError(t, err)
	require.True(t, proto.Equal(p1, result[0]))
	require.True(t, proto.Equal(p2, result[1]))
}

// ---------------------------------------------------------------------------
// Test helpers (error/bad-count drivers, codecs, callback)
// ---------------------------------------------------------------------------

// panicDriver panics in Store or Retrieve when the corresponding flag is set.
type panicDriver struct {
	name            string
	panicOnStore    bool
	panicOnRetrieve bool
}

func (d *panicDriver) Name() string { return d.name }
func (d *panicDriver) Type() string { return "panic" }
func (d *panicDriver) Store(_ converter.StorageDriverStoreContext, _ []*commonpb.Payload) ([]converter.StorageClaim, error) {
	if d.panicOnStore {
		panic("store panic")
	}
	return nil, nil
}
func (d *panicDriver) Retrieve(_ converter.StorageDriverRetrieveContext, _ []converter.StorageClaim) ([]*commonpb.Payload, error) {
	if d.panicOnRetrieve {
		panic("retrieve panic")
	}
	return nil, nil
}

// blockingDriver blocks its Store/Retrieve call until the supplied context is
// cancelled, then records the cancellation on cancelledCh.
type blockingDriver struct {
	name        string
	cancelledCh chan struct{}
}

func (d *blockingDriver) Name() string { return d.name }
func (d *blockingDriver) Type() string { return "blocking" }
func (d *blockingDriver) Store(ctx converter.StorageDriverStoreContext, _ []*commonpb.Payload) ([]converter.StorageClaim, error) {
	<-ctx.Context.Done()
	close(d.cancelledCh)
	return nil, ctx.Context.Err()
}
func (d *blockingDriver) Retrieve(ctx converter.StorageDriverRetrieveContext, _ []converter.StorageClaim) ([]*commonpb.Payload, error) {
	<-ctx.Context.Done()
	close(d.cancelledCh)
	return nil, ctx.Context.Err()
}

type funcDriverSelector struct {
	fn func(converter.StorageDriverStoreContext, *commonpb.Payload) (converter.StorageDriver, error)
}

func (s *funcDriverSelector) SelectDriver(ctx converter.StorageDriverStoreContext, p *commonpb.Payload) (converter.StorageDriver, error) {
	return s.fn(ctx, p)
}

type badCountDriver struct{ name string }

func (d *badCountDriver) Name() string { return d.name }
func (d *badCountDriver) Type() string { return "bad" }
func (d *badCountDriver) Store(_ converter.StorageDriverStoreContext, _ []*commonpb.Payload) ([]converter.StorageClaim, error) {
	return []converter.StorageClaim{}, nil // returns 0 claims instead of 1
}
func (d *badCountDriver) Retrieve(_ converter.StorageDriverRetrieveContext, _ []converter.StorageClaim) ([]*commonpb.Payload, error) {
	return nil, nil
}

type badCountRetrieveDriver struct{ name string }

func (d *badCountRetrieveDriver) Name() string { return d.name }
func (d *badCountRetrieveDriver) Type() string { return "bad" }
func (d *badCountRetrieveDriver) Store(_ converter.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]converter.StorageClaim, error) {
	claims := make([]converter.StorageClaim, len(payloads))
	for i := range claims {
		claims[i] = converter.StorageClaim{ClaimData: map[string]string{"key": "k"}}
	}
	return claims, nil
}
func (d *badCountRetrieveDriver) Retrieve(_ converter.StorageDriverRetrieveContext, _ []converter.StorageClaim) ([]*commonpb.Payload, error) {
	return []*commonpb.Payload{}, nil // returns 0 payloads instead of 1
}

// valueReceiverDriver implements StorageDriver entirely with value receivers.
// NewValDriver returns *valueReceiverDriver, so the interface holds a pointer
// (always comparable as a map key). Value receivers share the same underlying
// map reference, so Store/Retrieve mutations on the data map persist correctly.
type valueReceiverDriver struct {
	name string
	data map[string]*commonpb.Payload
}

func (d valueReceiverDriver) Name() string { return d.name }
func (d valueReceiverDriver) Type() string { return "value" }
func (d valueReceiverDriver) Store(_ converter.StorageDriverStoreContext, payloads []*commonpb.Payload) ([]converter.StorageClaim, error) {
	claims := make([]converter.StorageClaim, len(payloads))
	for i, p := range payloads {
		key := uuid.NewString()
		d.data[key] = proto.Clone(p).(*commonpb.Payload)
		claims[i] = converter.StorageClaim{ClaimData: map[string]string{"key": key}}
	}
	return claims, nil
}
func (d valueReceiverDriver) Retrieve(_ converter.StorageDriverRetrieveContext, claims []converter.StorageClaim) ([]*commonpb.Payload, error) {
	payloads := make([]*commonpb.Payload, len(claims))
	for i, c := range claims {
		p, ok := d.data[c.ClaimData["key"]]
		if !ok {
			return nil, fmt.Errorf("key not found: %q", c.ClaimData["key"])
		}
		payloads[i] = proto.Clone(p).(*commonpb.Payload)
	}
	return payloads, nil
}

func newValDriver(name string) converter.StorageDriver {
	return valueReceiverDriver{name: name, data: map[string]*commonpb.Payload{}}
}

type testCallback struct {
	mu                sync.Mutex
	count             int
	size              int64
	duration          time.Duration
	unconfiguredCount int
}

func (c *testCallback) PayloadBatchCompleted(count int, size int64, duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count = count
	c.size = size
	c.duration = duration
}

func (c *testCallback) UnconfiguredStorageReference() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unconfiguredCount++
}
