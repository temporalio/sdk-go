# Payload Serialization & the Nexus Data Flow

This document explains how the Temporal Go SDK turns Go values into
`commonpb.Payload` protobufs and back, how that machinery is used for **Nexus
operations** (from a caller invoking an operation all the way to the handler
worker executing it), whether any type information travels with the data, and
how custom payload encoding (compression/encryption codecs) plugs in.

> Scope note: file/line references point at this repository
> (`go.temporal.io/sdk`) at the current branch. External behavior in the
> Temporal **server** and in the `github.com/nexus-rpc/sdk-go` dependency is
> described where it matters, and called out as such.

---

## 1. The `Payload` wire format

Everything that crosses the Temporal wire — workflow args, activity args,
Nexus operation input/output, memo values, search attributes, failure details —
is a `commonpb.Payload`:

```proto
message Payload {
    map<string, bytes> metadata = 1;   // e.g. {"encoding": "json/plain"}
    bytes              data     = 2;    // the serialized bytes
}
```

Two things matter:

- **`data`** — the serialized bytes of the value.
- **`metadata`** — a string→bytes map. The single most important key is
  `"encoding"`. It is a *self-describing tag* that tells the deserializer which
  converter produced the bytes. A second key, `"messageType"`, is added for
  protobuf payloads.

The metadata keys are defined in `converter/metadata.go`:

| Constant | Value | Meaning |
|---|---|---|
| `MetadataEncoding` | `"encoding"` | which converter produced `data` |
| `MetadataMessageType` | `"messageType"` | proto message full name (proto encodings only) |
| `MetadataEncodingNil` | `"binary/null"` | a `nil` value |
| `MetadataEncodingBinary` | `"binary/plain"` | raw `[]byte` |
| `MetadataEncodingJSON` | `"json/plain"` | `encoding/json` |
| `MetadataEncodingProtoJSON` | `"json/protobuf"` | protojson |
| `MetadataEncodingProto` | `"binary/protobuf"` | binary protobuf |

**Key takeaway for the questions below:** the `"encoding"` tag describes the
*wire format* (JSON, proto, raw bytes, nil). For everything except protobuf, it
carries **no application-level type identity**. A `struct MyInputType{...}`
serialized to JSON is tagged `json/plain` — exactly the same tag a completely
different struct, a `map`, or a bare string would get.

---

## 2. The converter architecture

Serialization goes through a `converter.DataConverter`
(`converter/data_converter.go`). Its four core methods:

```go
ToPayload(value interface{}) (*commonpb.Payload, error)
FromPayload(payload *commonpb.Payload, valuePtr interface{}) error
ToPayloads(value ...interface{}) (*commonpb.Payloads, error)
FromPayloads(payloads *commonpb.Payloads, valuePtrs ...interface{}) error
```

Note the distinction between `Payload` (singular — one value) and `Payloads`
(a list, e.g. the positional arguments of a function). **Nexus operations use
the singular `Payload`** — a Nexus operation has exactly one input and one
output value (see §4).

### 2.1 `CompositeDataConverter` — the default

`GetDefaultDataConverter()` (`converter/default_data_converter.go`) is a
`CompositeDataConverter` composed of an **ordered** list of `PayloadConverter`s:

```go
NewCompositeDataConverter(
    NewNilPayloadConverter(),        // binary/null
    NewByteSlicePayloadConverter(),  // binary/plain
    NewProtoJSONPayloadConverter(),  // json/protobuf
    NewProtoPayloadConverter(),      // binary/protobuf
    NewJSONPayloadConverter(),       // json/plain  (catch-all, last)
)
```

Each `PayloadConverter` (`converter/payload_converter.go`) implements:

```go
ToPayload(value interface{}) (*commonpb.Payload, error) // nil,nil if it can't handle the value
FromPayload(payload *commonpb.Payload, valuePtr interface{}) error
Encoding() string  // the "encoding" tag it owns
```

**Serialization (`ToPayload`)** — `CompositeDataConverter.ToPayload`
(`converter/composite_data_converter.go`) tries converters *in order* and uses
the first one that returns a non-nil payload:

```go
for _, enc := range dc.orderedEncodings {
    payload, err := dc.payloadConverters[enc].ToPayload(value)
    if err != nil { return nil, err }
    if payload != nil { return payload, nil }   // first match wins
}
return nil, ErrUnableToFindConverter
```

Order matters: `nil` is caught first, then `[]byte`, then proto messages, and
finally the JSON converter — which is the catch-all, since `json.Marshal`
succeeds on almost any Go value.

**Deserialization (`FromPayload`)** — this is where the self-describing
`"encoding"` tag drives everything:

```go
enc, err := encoding(payload)              // read payload.Metadata["encoding"]
payloadConverter, ok := dc.payloadConverters[enc]
if !ok { return ErrEncodingIsNotSupported }
return payloadConverter.FromPayload(payload, valuePtr)
```

So the *bytes* are decoded according to the tag the *producer* wrote — never
"guessed" from the target Go type. If the tag names an encoding this converter
wasn't configured with, you get `ErrEncodingIsNotSupported`.

### 2.2 What each converter does with types

- **`JSONPayloadConverter`** (`converter/json_payload_converter.go`) — plain
  `json.Marshal` / `json.Unmarshal`. `FromPayload` is literally
  `json.Unmarshal(payload.GetData(), valuePtr)`. **No type tag is stored or
  checked.** This is the converter used for ordinary user structs like
  `MyInputType`.
- **`ProtoPayloadConverter`** / **`ProtoJSONPayloadConverter`**
  (`converter/proto_payload_converter.go`, `proto_json_payload_converter.go`) —
  only for values implementing `proto.Message` / `gogoproto.Message`. These
  **do** record a type: `newProtoPayload(...)` writes
  `Metadata["messageType"] = "my.package.MyMessage"` (unless
  `ExcludeProtobufMessageTypes` is set). But note that even here, `FromPayload`
  does **not** verify that `messageType` matches the target — it just calls
  `proto.Unmarshal` into whatever `*T` you passed. The `messageType` metadata
  exists primarily for tooling/UI and dynamic-message scenarios, not for
  handshake validation.
- **`NilPayloadConverter`**, **`ByteSlicePayloadConverter`** — handle `nil` and
  `[]byte` respectively.

### 2.3 `RawValue` escape hatch

If a value is a `converter.RawValue`, the composite converter passes the raw
`*commonpb.Payload` through untouched (encoding still happens for codecs, but
data conversion is skipped). Used when you want to move a payload without
interpreting it.

---

## 3. Custom encoding: `PayloadCodec` (compression / encryption)

There are two distinct layers, and it's important not to conflate them:

1. **`PayloadConverter`** — turns a *Go value* into *bytes + an encoding tag*
   (JSON, proto, …). This is where you'd register handling for a new **type**.
2. **`PayloadCodec`** — a byte-to-byte transform applied *after* conversion:
   compression, **encryption**, signing, etc. It operates on whole
   `*commonpb.Payload`s and neither knows nor cares about Go types.

### 3.1 `PayloadCodec`

`converter/codec.go`:

```go
type PayloadCodec interface {
    Encode([]*commonpb.Payload) ([]*commonpb.Payload, error)
    Decode([]*commonpb.Payload) ([]*commonpb.Payload, error)
}
```

You wrap a base `DataConverter` with `NewCodecDataConverter(parent, codecs...)`.
The result is a `CodecDataConverter` that:

- on `ToPayload(s)` — first calls the parent converter to get payloads, then
  applies codecs **last-to-first** (so earlier codecs wrap later ones);
- on `FromPayload(s)` and `ToString(s)` — applies codecs **first-to-last** to
  reverse the effect, then hands the decoded payloads to the parent.

A typical codec rewrites `Metadata["encoding"]` to its own tag so it can
recognize its own output on the way back. The bundled `zlibCodec` sets
`Metadata["encoding"] = "binary/zlib"` and, on decode, only touches payloads
carrying that tag — leaving foreign payloads alone. An **encryption codec**
follows the same pattern (encrypt bytes, tag as e.g. `binary/encrypted`,
decrypt only its own tag). This is exactly how the "transparent
encryption/decryption" you described works: the codec sits inside the
`DataConverter`, so every payload the SDK sends is encrypted and every payload
it receives is decrypted, with the rest of the SDK unaware.

### 3.2 Remote codec

`NewRemoteDataConverter` / `NewRemotePayloadCodec` (also `codec.go`) send
payloads over HTTP to an external "codec server" for encode/decode — the
mechanism behind Temporal's remote-codec / codec-server feature that lets the
Web UI and CLI display decrypted payloads without holding the keys locally.
`NewPayloadCodecHTTPHandler` is the server side of that contract.

### 3.3 Where the converter is configured

The `DataConverter` (optionally codec-wrapped) is set once on
`client.Options.DataConverter`. If unset it defaults to
`converter.GetDefaultDataConverter()`. Both the **caller** side and the
**handler** side pick it up from their respective clients (see §4 and §5), so a
custom codec must be configured on **both** clients for the round trip to work.

---

## 4. Caller side: invoking a Nexus operation

There are **two** call paths that both end up producing a single input
`*commonpb.Payload`.

### 4.1 From inside a workflow — `workflow.ExecuteNexusOperation`

This is the common case. Flow:

```
workflow code
  → NexusClient.ExecuteOperation(ctx, operation, input, options)   internal/workflow.go:3103
  → outbound interceptor .ExecuteNexusOperation
  → prepareNexusOperationParams(ctx, input)                        internal/workflow.go:3115
  → dc.ToPayload(input.Input)                                      internal/workflow.go:3134
  → workflowEnvironmentImpl.ExecuteNexusOperation(params, ...)     internal/internal_event_handlers.go:639
  → ScheduleNexusOperationCommandAttributes{ Input: params.input } internal/internal_event_handlers.go:641
```

Two things happen in `prepareNexusOperationParams`
(`internal/workflow.go:3115`):

1. **A local, caller-only type check.** If `operation` is a *typed*
   `OperationReference` (something with `Name() string` and
   `InputType() reflect.Type`), the SDK verifies the argument is assignable to
   the declared input type:

   ```go
   inputType := reflect.TypeOf(input.Input)
   if inputType != nil && !inputType.AssignableTo(regOp.InputType()) {
       return ..., fmt.Errorf("cannot assign argument of type %q to type %q ...")
   }
   ```

   If `operation` is passed as a bare **string**, there is **no** check at all.
   Either way, this only compares the argument against the *caller's own* view
   of the operation — it does **not** consult the handler.

2. **Serialization to a single payload:**

   ```go
   payload, err := dc.ToPayload(input.Input)   // singular Payload, not Payloads
   ```

The payload is placed on a `ScheduleNexusOperation` **command**, which the SDK
returns to the server inside `RespondWorkflowTaskCompleted` (gRPC). The workflow
never talks Nexus HTTP itself — it emits a command and the **server** drives the
operation.

### 4.2 Standalone client — `client.NexusClient.ExecuteOperation`

`internal/internal_nexus_client.go`. Same idea, different transport:

```go
// workflowClientInterceptor.ExecuteNexusOperation, internal_nexus_client.go:670
dataConverter := WithContext(ctx, w.client.dataConverter)  // falls back to default
inputPayload, err := dataConverter.ToPayload(in.Input)     // "Encode input as a single Payload (not Payloads)"
request := &workflowservice.StartNexusOperationExecutionRequest{
    ...
    Input: inputPayload,
}
resp, err := w.client.WorkflowService().StartNexusOperationExecution(grpcCtx, request)
```

`resolveNexusOperationName` (`internal_nexus_client.go:451`) performs the same
local `AssignableTo` check as the workflow path, and the same "no check for
string operation names" rule applies.

### 4.3 What travels on the wire

Either way the caller sends **one `commonpb.Payload`** (with its `encoding`
metadata) to the Temporal frontend — embedded in a workflow command, or as the
`Input` field of `StartNexusOperationExecutionRequest`. No Go type name is sent
for JSON payloads; only the `encoding` tag (and `messageType` for protos).

---

## 5. The server's role (Payload ↔ Nexus HTTP Content)

The Temporal **server** is the Nexus caller/orchestrator over the wire. When it
delivers the operation to the handler's worker it does **not** re-run Go
serialization — it converts between the Nexus HTTP representation
(`nexus.Content`: an HTTP-header map + a body) and `commonpb.Payload`, then
dispatches a task on the handler's Nexus task queue.

The header⇄metadata convention (implemented in the server and in the
`nexus-rpc/sdk-go` built-in serializers, **not** in this repo) is a `content-`
prefix: an HTTP `Content-Type: application/json` becomes the nexus
`Content.Header["type"]`, and payload metadata such as `encoding` /
`messageType` ride along as `Content-Encoding` / `Content-MessageType`-style
headers. This repo documents the reserved prefix in
`temporalnexus/temporal_operation.go:22`:

```go
// Header keys with the "content-" prefix are reserved for [nexus.Serializer] headers ...
```

The practical consequence: by the time a payload reaches the handler worker, it
is already a `commonpb.Payload` again — which is why the SDK's handler-side
serializer (below) ignores the `nexus.Content` entirely.

---

## 6. Handler side: executing a Nexus operation

The handler worker long-polls `PollNexusTaskQueue` and runs the user's
operation. Full chain:

```
nexusTaskPoller.poll()                                  internal/internal_nexus_task_poller.go
  → WorkflowService.PollNexusTaskQueue (gRPC)
  → nexusTaskPoller.ProcessTask
  → nexusTaskHandler.ExecuteContext → execute           internal/internal_nexus_task_handler.go:143
  → handleStartOperation(...)                           internal/internal_nexus_task_handler.go:168
```

### 6.1 The `payloadSerializer` shim

`handleStartOperation` does **not** deserialize the input itself. It wraps the
incoming `*commonpb.Payload` in a "fake" `nexus.Serializer` and a `LazyValue`
(`internal_nexus_task_handler.go:174`):

```go
serializer := &payloadSerializer{
    converter: h.dataConverter,
    payload:   req.GetPayload(),
}
// Create a fake lazy value, Temporal server already converts Nexus content into payloads.
input := nexus.NewLazyValue(serializer, &nexus.Reader{ReadCloser: emptyReaderNopCloser})
...
opres, err = h.nexusHandler.StartOperation(ctx, req.GetService(), req.GetOperation(), input, startOptions)
```

`payloadSerializer` (`internal_nexus_task_handler.go:604`) is intentionally
thin — it **ignores** the `nexus.Content` (the reader is empty) and reads the
embedded payload through the Temporal `DataConverter`:

```go
func (p *payloadSerializer) Deserialize(_ *nexus.Content, v any) error {
    return p.converter.FromPayload(p.payload, v)   // <-- the actual deserialization
}
func (p *payloadSerializer) Serialize(v any) (*nexus.Content, error) {
    panic("unimplemented") // outputs are serialized to payload directly (see §6.3)
}
```

### 6.2 How the typed input `MyInputType` is produced (nexus-rpc SDK)

The typed value is materialized by the `github.com/nexus-rpc/sdk-go` dependency
using **reflection on the handler's own signature** — the handler's declared
input type is the single source of truth. In
`nexus-rpc/sdk-go/nexus/operation.go` (`registryHandler.StartOperation`):

```go
m, _ := reflect.TypeOf(ro).MethodByName("Start")
inputType := m.Type.In(2)                 // the handler's I parameter type (e.g. MyInputType)
iptr := reflect.New(inputType).Interface() // allocate a *MyInputType
if err := input.Consume(iptr); err != nil {
    return nil, NewHandlerErrorf(HandlerErrorTypeBadRequest, "invalid input")
}
return h.Start(ctx, reflect.ValueOf(iptr).Elem().Interface(), options)
```

`LazyValue.Consume(iptr)` (`nexus/serializer.go`) calls
`serializer.Deserialize(content, iptr)` → our `payloadSerializer.Deserialize` →
`dataConverter.FromPayload(payload, iptr)`. The filled-in value is then passed
to the user's operation:

```go
func (o *workflowRunOperation[I, O]) Start(ctx, input I, options) ...   // temporalnexus/operation.go:170
```

So the operation's generic type parameter `I` (e.g. `MyInputType`) determines
the target of `FromPayload`. There is no negotiation with the caller — the
handler declares the type, and the caller's bytes are decoded into it.

### 6.3 Output (handler → caller)

The result is serialized directly with the data converter — `Serialize` is never
called, which is why it panics (`internal_nexus_task_handler.go:340`):

```go
value := reflect.ValueOf(t).Elem().FieldByName("Value").Interface()
payload, err := h.dataConverter.ToPayload(value)
... StartOperationResponse_Sync{ Payload: payload } ...
```

The caller receives that payload back (workflow path: via the operation
completion event / future's `Get`; standalone path:
`PollNexusOperationExecution` → `EncodedValue.Get` →
`FromPayload`).

### 6.4 Where the handler-side converter comes from

The `DataConverter` used by `payloadSerializer` is the worker's, which is the
client's: `client.dataConverter` → worker execution params
(`DataConverter: client.dataConverter`, defaulting to
`GetDefaultDataConverter()`) → `newNexusWorker` →
`newNexusTaskHandler(..., opts.executionParameters.DataConverter, ...)`
(`internal/internal_nexus_worker.go:39`) → `nexusTaskHandler.dataConverter`.

---

## 7. Answering the specific questions

### Q: Does the SDK pass type information so caller and handler "match"?

**Not in any way that is checked across the two sides.** For ordinary Go types
(the JSON path), the payload carries only `encoding = json/plain` plus the raw
JSON bytes. There is no Go type name, no schema, no message identifier. For
protobuf types the payload additionally carries
`messageType = "my.package.MyMessage"`, but even that is **not verified** on
deserialization — it's informational (tooling/UI/dynamic decoding).

The only type checks that exist are **local to the caller** and only fire when
you invoke via a *typed* `OperationReference` (not a string name):
`reflect.TypeOf(input).AssignableTo(regOp.InputType())` in
`prepareNexusOperationParams` (`internal/workflow.go:3127`) and
`resolveNexusOperationName` (`internal/internal_nexus_client.go:464`). This
compares your argument against *your own* declaration of the operation; it never
contacts the handler and does nothing if you call by string.

### Q: Can you call an operation with a completely different type? Does it fail at runtime?

**Yes, you can send a different type, and yes, mismatches surface at runtime on
the handler** — not before. The handler picks its expected type by reflecting on
its own `Start` signature and calls `FromPayload` into that type:

- If the bytes can't be decoded into the handler's type,
  `FromPayload`/`json.Unmarshal` errors. The nexus-rpc SDK turns any `Consume`
  error into `HandlerErrorTypeBadRequest` with message `"invalid input"`
  (`nexus/operation.go`). That comes back to the caller as a Nexus handler
  error.
- If the encoding tag itself is one the handler's converter doesn't know, you
  get `ErrEncodingIsNotSupported`.

Crucially, **JSON deserialization is lenient**: `encoding/json` ignores unknown
fields and zero-fills missing ones by default. So calling with a *structurally
overlapping but semantically different* type often does **not** error — it
silently produces a partially/oddly populated `MyInputType`. You only get a hard
runtime failure when the JSON is structurally incompatible (e.g. a JSON array
where a struct is expected, or a string where a number is expected). There is no
safety net that says "these are different types."

### Q: How does custom payload encoding/encryption fit in?

Via `PayloadCodec` wrapped around the `DataConverter` with
`NewCodecDataConverter` (§3). The codec transforms the payload **bytes** after
type conversion (encrypt/compress) and reverses it before type conversion on the
way in, re-tagging `Metadata["encoding"]` so it recognizes its own output. It is
transparent to all the Nexus code above because it lives *inside* the
`DataConverter` that both the caller and handler already use. For a Nexus round
trip to work, configure the same codec-wrapped converter on both the caller's
client and the handler worker's client. (`NewRemoteDataConverter` offloads this
to an HTTP codec server, e.g. so the Web UI can display decrypted values.)

---

## 8. Raw / type-agnostic handlers (`converter.RawValue`)

Sometimes you want a handler that accepts *any* input type — callers may invoke
it with a `string`, an `int`, a custom `MyType` struct, or a protobuf — and you
want the handler to receive the **raw payload** rather than a deserialized Go
value. This is possible with **no SDK changes** using `converter.RawValue`.

### Why it works

Recall the handler-side chain (§6.2): the nexus-rpc SDK reflects on the
handler's `Start` signature to get the input type `I`, does `reflect.New(I)` to
make a `*I`, and calls `FromPayload(payload, thatPointer)`. The very first thing
`CompositeDataConverter.FromPayload` does is special-case `*RawValue`
(`converter/composite_data_converter.go:105`):

```go
rawValue, ok := valuePtr.(*RawValue)
if ok {
    *rawValue = NewRawValue(payload)   // wrap the raw payload, return. No unmarshalling.
    return nil
}
```

So declaring the operation's input type as `converter.RawValue` makes `*I` a
`*converter.RawValue`, this branch fires, and the handler receives the raw
`*commonpb.Payload` untouched. `RawValue` (`converter/value.go`) is a thin
wrapper with a `Payload() *commonpb.Payload` accessor. `nexus.NewSyncOperation`
places no constraint on `I` (`[I, O any]`), so `I = converter.RawValue` is
legal.

> **`*commonpb.Payload` as the input type does NOT work** as a bypass. It is not
> special-cased, so the composite converter treats it as an ordinary target:
> it reads the `encoding` tag and tries to unmarshal into a `Payload` proto
> (e.g. `json.Unmarshal` for a `json/plain` payload) — producing garbage or an
> error. `RawValue` is the only built-in escape hatch.

### Handler side

```go
import "go.temporal.io/sdk/converter"

op := nexus.NewSyncOperation("passthrough",
    func(ctx context.Context, input converter.RawValue, _ nexus.StartOperationOptions) (SomeOutput, error) {
        p := input.Payload()                    // *commonpb.Payload, verbatim
        enc := string(p.Metadata["encoding"])   // e.g. "json/plain", "binary/protobuf"

        // Option A: forward / store the bytes, or route on `enc`.
        // Option B: decode on demand once you've decided the target type:
        var name string
        _ = converter.GetDefaultDataConverter().FromPayload(p, &name)
        ...
    },
)
```

### Caller side — accepting any input type

Callers pass their natural Go value; their own `DataConverter.ToPayload`
serializes it the usual way (`json/plain` for `string`/`int`/`MyType`,
`binary/protobuf` for a proto). The one thing to steer around is the
**caller-side `AssignableTo` check** (§4.1): invoke **by string operation name**
so the check is skipped and any input type is accepted.

Do **not** hand callers a typed `OperationReference` built from this op — its
`InputType()` is `converter.RawValue`, and e.g.
`reflect.TypeOf("hello").AssignableTo(RawValue)` is false, so a plain string
would be rejected before it ever leaves the caller.

### Caveats

1. **Codecs still run; unmarshalling doesn't.** If the converter is wrapped in a
   `CodecDataConverter` (compression/encryption), `RawValue` yields the
   *decoded* (decompressed/decrypted) payload — the codec `Decode` runs first,
   then the parent wraps the result. Only the type-level marshalling is skipped.
   This is by design (the `DataConverter` interface doc: *"When valuePtr is of
   RawValue type, decryption should occur but data conversion must be
   skipped"*) and is usually what you want: clean, usable bytes plus the
   `encoding` tag.
2. **A RawValue-aware converter is required.** The default converter and the
   bundled `CodecDataConverter`/`remoteDataConverter` all honor `RawValue`. A
   fully custom `DataConverter` that doesn't special-case `*RawValue` would
   break this — so it depends on the worker's configured converter (fine for
   the vast majority of setups).

`RawValue` is symmetric: returning a `converter.RawValue` from the handler (or
passing one as caller input) is passed through by `ToPayload` the same way
(`composite_data_converter.go:84`), so you can also emit a pre-built payload
without re-serialization.

---

## 9. End-to-end sequence (workflow-driven Nexus operation)

```
CALLER WORKFLOW (worker A)                     TEMPORAL SERVER                 HANDLER (worker B)
──────────────────────────                     ───────────────                 ─────────────────
workflow.ExecuteNexusOperation(op, MyInputType{...})
  prepareNexusOperationParams
    [optional local AssignableTo check]
    dc.ToPayload(input)  ─────────►  Payload{meta:{encoding:json/plain}, data: <json bytes>}
  ScheduleNexusOperation command
        │  RespondWorkflowTaskCompleted (gRPC)
        └───────────────────────────►  server holds the Payload
                                        converts Payload → nexus Content (HTTP headers)
                                        dispatches Nexus task to handler's task queue
                                                                     ◄──── PollNexusTaskQueue (gRPC)
                                        Content → Payload (again)  ──►  nexuspb.StartOperationRequest{Payload}
                                                                     handleStartOperation
                                                                       payloadSerializer{payload}
                                                                       nexus.NewLazyValue(...)
                                                                       h.nexusHandler.StartOperation
                                                                     nexus-rpc SDK:
                                                                       inputType = reflect(Start.In(2))
                                                                       iptr = new(MyInputType)
                                                                       LazyValue.Consume(iptr)
                                                                         → payloadSerializer.Deserialize
                                                                           → dc.FromPayload(payload, iptr)
                                                                             → json.Unmarshal → MyInputType
                                                                       user Start(ctx, MyInputType, opts)
                                                                     dc.ToPayload(result) ──► response Payload
        result Payload  ◄──────────────  server relays completion  ◄──────────  RespondNexusTaskCompleted
  future.Get(&out): dc.FromPayload(resultPayload, &out)
```

---

## 10. Key files

| Concern | File |
|---|---|
| `DataConverter` interface | `converter/data_converter.go` |
| Default composite converter | `converter/default_data_converter.go` |
| Composite dispatch (encoding-tag routing) | `converter/composite_data_converter.go` |
| Encoding/messageType metadata keys | `converter/metadata.go` |
| JSON converter (the user-struct path) | `converter/json_payload_converter.go` |
| Proto / proto-JSON converters (`messageType`) | `converter/proto_payload_converter.go`, `converter/proto_json_payload_converter.go` |
| Codecs (compression/encryption, remote) | `converter/codec.go` |
| Caller — workflow path | `internal/workflow.go` (`prepareNexusOperationParams`), `internal/internal_event_handlers.go` (`ExecuteNexusOperation`) |
| Caller — standalone client path | `internal/internal_nexus_client.go` |
| Handler — task poll/dispatch | `internal/internal_nexus_task_poller.go` |
| Handler — start op + `payloadSerializer` | `internal/internal_nexus_task_handler.go` |
| Nexus operation registration (`I`,`O`) | `temporalnexus/operation.go` |
| Reflection that produces typed input | `github.com/nexus-rpc/sdk-go/nexus/operation.go`, `.../nexus/serializer.go` (dependency) |
</content>
</invoke>
