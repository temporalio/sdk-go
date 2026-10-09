// Command payload-limit-gen generates the payloadlimits validator from the go.temporal.io/api proto descriptors
// linked into this binary, so the output always matches the API version in go.mod.
//
// Starting from the request message of every WorkflowService and OperatorService RPC, it walks the
// payload-bearing message closure, stopping at terminal payload and memo types, and emits one check
// function per message plus a type switch over the request roots. Each terminal field is
// classified by the decision table in table.go; how it's measured follows from its proto shape.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"

	_ "go.temporal.io/api/operatorservice/v1"
	_ "go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const (
	payloadType          = "temporal.api.common.v1.Payload"
	payloadsType         = "temporal.api.common.v1.Payloads"
	memoType             = "temporal.api.common.v1.Memo"
	headerType           = "temporal.api.common.v1.Header"
	searchAttributesType = "temporal.api.common.v1.SearchAttributes"
	failureType          = "temporal.api.failure.v1.Failure"
	anyType              = "google.protobuf.Any"
)

// The walk stops at these types and checks the holding field instead of descending. Failure is
// measured whole because that's how the server size-checks it. Any is measured whole too: its
// contents are opaque here and may hold payloads, so every Any field must be classified.
var terminalLeaves = map[protoreflect.FullName]bool{
	payloadType: true, payloadsType: true, memoType: true,
	headerType: true, searchAttributesType: true, failureType: true, anyType: true,
}

var seedFiles = []string{
	"temporal/api/workflowservice/v1/service.proto",
	"temporal/api/operatorservice/v1/service.proto",
}

type decisionTable struct {
	blobFields         []string
	memoFields         []string
	blobWarnFields     []string
	notValidatedFields []string
}

type limitClass int

const (
	classBlob limitClass = iota
	classMemo
)

type fieldPolicy struct {
	validated    bool
	class        limitClass
	enforceError bool
}

type leafKind int

const (
	leafPayloads leafKind = iota
	leafPayload
	leafRepeatedPayload
	leafMemo
	// leafMemoDataSum is a Memo checked against the blob limit, measured as its fields' data-sum.
	leafMemoDataSum
	leafHeader
	leafSearchAttributes
	leafMapPayload
	leafMapPayloads
	leafWholeMessage
	leafRepeatedWholeMessage
)

type structShape int

const (
	shapeSingle structShape = iota + 1
	shapeRepeated
	shapeMap
)

// target is where a field leads: a measured leaf, a nested message to recurse into, or neither.
type target struct {
	isLeaf bool
	leaf   leafKind
	shape  structShape
	child  protoreflect.MessageDescriptor
}

func main() {
	out := flag.String("out", "", "validator file to write, or with -check to compare against")
	check := flag.Bool("check", false, "verify the decision table and that -out is up to date, without writing")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: payload-limit-gen [-check] -out <file>")
		os.Exit(2)
	}
	src, err := generate(defaultTable)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		got, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !bytes.Equal(got, src) {
			fmt.Fprintf(os.Stderr, "payload-limits: %s is out of date; run `go generate ./internal/payloadlimits`\n", *out)
			os.Exit(1)
		}
		return
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type generator struct {
	table        map[string]fieldPolicy
	reaches      map[protoreflect.FullName]bool
	validating   map[protoreflect.FullName]bool
	usedKeys     map[string]bool
	unclassified map[string]bool
}

func generate(table decisionTable) ([]byte, error) {
	g := &generator{
		reaches:      map[protoreflect.FullName]bool{},
		usedKeys:     map[string]bool{},
		unclassified: map[string]bool{},
	}
	var err error
	if g.table, err = loadTable(table); err != nil {
		return nil, err
	}
	var seeds []protoreflect.FileDescriptor
	for _, path := range seedFiles {
		fd, err := protoregistry.GlobalFiles.FindFileByPath(path)
		if err != nil {
			return nil, fmt.Errorf("payload-limits: seed file %s not registered: %w", path, err)
		}
		seeds = append(seeds, fd)
	}
	g.computeReachability(collectMessages(fileClosure(seeds)))

	var roots []protoreflect.MessageDescriptor
	for _, fd := range seeds {
		services := fd.Services()
		for i := 0; i < services.Len(); i++ {
			methods := services.Get(i).Methods()
			for j := 0; j < methods.Len(); j++ {
				roots = append(roots, methods.Get(j).Input())
			}
		}
	}

	toGenerate := map[protoreflect.FullName]protoreflect.MessageDescriptor{}
	for _, md := range g.limitsClosure(roots) {
		toGenerate[md.FullName()] = md
	}

	// Classify every leaf before pruning so the table checks see every payload-bearing field,
	// whatever ends up emitted.
	for _, md := range toGenerate {
		for _, l := range g.leavesOf(md) {
			g.policy(leafKey(md, l.field))
		}
	}
	if len(g.unclassified) > 0 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "payload-limits: %d payload-bearing field(s) in the go.temporal.io/api version from go.mod "+
			"are not classified, usually because that version was just changed. Add each to the right list "+
			"(blobFields / memoFields / blobWarnFields / notValidatedFields) in internal/cmd/payload-limit-gen/table.go, "+
			"whose header explains how to choose, then run `go generate ./internal/payloadlimits`:\n", len(g.unclassified))
		for _, k := range sortedKeys(g.unclassified) {
			fmt.Fprintf(&sb, "\t%q,\n", k)
		}
		return nil, fmt.Errorf("%s", sb.String())
	}
	var stale []string
	for k := range g.table {
		if !g.usedKeys[k] {
			stale = append(stale, k)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		return nil, fmt.Errorf("payload-limits: %d entr(y/ies) in internal/cmd/payload-limit-gen/table.go no longer "+
			"correspond to a payload-bearing field in the go.temporal.io/api version from go.mod, usually because that "+
			"version was just changed; remove them, then run `go generate ./internal/payloadlimits`:\n\t%s",
			len(stale), strings.Join(stale, "\n\t"))
	}

	// Classification is done, so pruning messages that can never produce a check cannot weaken
	// the table checks above.
	g.validating = g.validatingClosure(toGenerate)
	for name := range toGenerate {
		if !g.validating[name] {
			delete(toGenerate, name)
		}
	}
	return g.emit(toGenerate, roots)
}

func loadTable(t decisionTable) (map[string]fieldPolicy, error) {
	m := map[string]fieldPolicy{}
	for _, group := range []struct {
		paths  []string
		policy fieldPolicy
	}{
		{t.blobFields, fieldPolicy{validated: true, class: classBlob, enforceError: true}},
		{t.memoFields, fieldPolicy{validated: true, class: classMemo, enforceError: true}},
		{t.blobWarnFields, fieldPolicy{validated: true, class: classBlob}},
		{t.notValidatedFields, fieldPolicy{}},
	} {
		for _, p := range group.paths {
			if _, dup := m[p]; dup {
				return nil, fmt.Errorf("payload-limits: duplicate table entry %q", p)
			}
			m[p] = group.policy
		}
	}
	return m, nil
}

func leafKey(md protoreflect.MessageDescriptor, f protoreflect.FieldDescriptor) string {
	return string(md.FullName()) + "." + string(f.Name())
}

func (g *generator) policy(key string) (fieldPolicy, bool) {
	p, ok := g.table[key]
	if !ok {
		g.unclassified[key] = true
		return fieldPolicy{}, false
	}
	g.usedKeys[key] = true
	return p, true
}

// --- Reachability ---

func fileClosure(seeds []protoreflect.FileDescriptor) []protoreflect.FileDescriptor {
	seen := map[string]bool{}
	var out []protoreflect.FileDescriptor
	queue := slices.Clone(seeds)
	for len(queue) > 0 {
		fd := queue[0]
		queue = queue[1:]
		if seen[fd.Path()] {
			continue
		}
		seen[fd.Path()] = true
		out = append(out, fd)
		imports := fd.Imports()
		for i := 0; i < imports.Len(); i++ {
			queue = append(queue, imports.Get(i).FileDescriptor)
		}
	}
	return out
}

func collectMessages(files []protoreflect.FileDescriptor) []protoreflect.MessageDescriptor {
	var out []protoreflect.MessageDescriptor
	var walk func(protoreflect.MessageDescriptors)
	walk = func(mds protoreflect.MessageDescriptors) {
		for i := 0; i < mds.Len(); i++ {
			md := mds.Get(i)
			if md.IsMapEntry() {
				continue
			}
			out = append(out, md)
			walk(md.Messages())
		}
	}
	for _, fd := range files {
		walk(fd.Messages())
	}
	return out
}

// valueMessage is the message type a field holds, unwrapping map values; nil for scalar fields.
func valueMessage(f protoreflect.FieldDescriptor) protoreflect.MessageDescriptor {
	if f.IsMap() {
		return f.MapValue().Message()
	}
	return f.Message()
}

func isTemporal(md protoreflect.MessageDescriptor) bool {
	return strings.HasPrefix(string(md.FullName()), "temporal.")
}

// computeReachability finds the messages that can transitively hold a payload, as a least fixpoint
// so cycles such as Failure.cause resolve correctly. Any counts as payload-bearing because its
// contents are opaque here, matching the Java SDK's payload visitor.
func (g *generator) computeReachability(all []protoreflect.MessageDescriptor) {
	children := map[protoreflect.FullName][]protoreflect.FullName{}
	for _, md := range all {
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			v := valueMessage(fields.Get(i))
			if v == nil {
				continue
			}
			switch v.FullName() {
			case payloadType, payloadsType, anyType:
				g.reaches[md.FullName()] = true
			default:
				if isTemporal(v) {
					children[md.FullName()] = append(children[md.FullName()], v.FullName())
				}
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, md := range all {
			if g.reaches[md.FullName()] {
				continue
			}
			for _, c := range children[md.FullName()] {
				if g.reaches[c] {
					g.reaches[md.FullName()] = true
					changed = true
					break
				}
			}
		}
	}
}

func (g *generator) included(md protoreflect.MessageDescriptor) bool {
	return g.reaches[md.FullName()]
}

func (g *generator) limitsClosure(roots []protoreflect.MessageDescriptor) []protoreflect.MessageDescriptor {
	var out []protoreflect.MessageDescriptor
	seen := map[protoreflect.FullName]bool{}
	queue := slices.Clone(roots)
	for len(queue) > 0 {
		md := queue[0]
		queue = queue[1:]
		if terminalLeaves[md.FullName()] || seen[md.FullName()] || !g.included(md) {
			continue
		}
		seen[md.FullName()] = true
		out = append(out, md)
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			if t := classify(fields.Get(i)); t.shape != 0 && g.included(t.child) {
				queue = append(queue, t.child)
			}
		}
	}
	return out
}

func classify(f protoreflect.FieldDescriptor) target {
	if f.IsMap() {
		v := f.MapValue().Message()
		switch {
		case v == nil:
			return target{}
		case v.FullName() == payloadType:
			return target{isLeaf: true, leaf: leafMapPayload}
		case v.FullName() == payloadsType:
			return target{isLeaf: true, leaf: leafMapPayloads}
		case v.FullName() == anyType:
			// Skipping it would let an opaque, possibly payload-bearing field go unclassified, and
			// no map of Any exists yet to decide how the server measures one.
			panic(fmt.Sprintf("payload-limits: map field %s holds google.protobuf.Any values, which the "+
				"generator cannot measure yet; add a leafKind for it", f.FullName()))
		case isTemporal(v):
			return target{shape: shapeMap, child: v}
		}
		return target{}
	}
	md := f.Message()
	if md == nil {
		return target{}
	}
	repeated := f.IsList()
	switch md.FullName() {
	case payloadType:
		if repeated {
			return target{isLeaf: true, leaf: leafRepeatedPayload}
		}
		return target{isLeaf: true, leaf: leafPayload}
	case payloadsType:
		return target{isLeaf: true, leaf: leafPayloads}
	case memoType:
		return target{isLeaf: true, leaf: leafMemo}
	case headerType:
		return target{isLeaf: true, leaf: leafHeader}
	case searchAttributesType:
		return target{isLeaf: true, leaf: leafSearchAttributes}
	case failureType, anyType:
		if repeated {
			return target{isLeaf: true, leaf: leafRepeatedWholeMessage}
		}
		return target{isLeaf: true, leaf: leafWholeMessage}
	}
	if !isTemporal(md) {
		return target{}
	}
	if repeated {
		return target{shape: shapeRepeated, child: md}
	}
	return target{shape: shapeSingle, child: md}
}

type leaf struct {
	field protoreflect.FieldDescriptor
	kind  leafKind
}

// leavesOf lists the measured fields of md in field order.
func (g *generator) leavesOf(md protoreflect.MessageDescriptor) []leaf {
	var out []leaf
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		if t := classify(fields.Get(i)); t.isLeaf {
			out = append(out, leaf{fields.Get(i), t.leaf})
		}
	}
	return out
}

// validatingClosure is the set of messages that can produce at least one check: those with a
// validated leaf, plus those that lead to one. Emitting the rest would only cost code size and
// per-request work.
func (g *generator) validatingClosure(toGenerate map[protoreflect.FullName]protoreflect.MessageDescriptor) map[protoreflect.FullName]bool {
	out := map[protoreflect.FullName]bool{}
	children := map[protoreflect.FullName][]protoreflect.FullName{}
	for name, md := range toGenerate {
		for _, l := range g.leavesOf(md) {
			if g.table[leafKey(md, l.field)].validated {
				out[name] = true
				break
			}
		}
		fields := md.Fields()
		for i := 0; i < fields.Len(); i++ {
			if t := classify(fields.Get(i)); t.shape != 0 && g.included(t.child) {
				children[name] = append(children[name], t.child.FullName())
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for name, refs := range children {
			if out[name] {
				continue
			}
			for _, c := range refs {
				if out[c] {
					out[name] = true
					changed = true
					break
				}
			}
		}
	}
	return out
}

// --- Emission ---

type goType struct {
	pkgPath string
	alias   string
	name    string
	ptr     reflect.Type
}

type emitter struct {
	buf     bytes.Buffer
	types   map[protoreflect.FullName]goType
	imports map[string]string // path -> alias
	aliases map[string]string // alias -> path
}

func (e *emitter) printf(format string, args ...any) {
	fmt.Fprintf(&e.buf, format, args...)
}

func (e *emitter) goType(md protoreflect.MessageDescriptor) (goType, error) {
	if t, ok := e.types[md.FullName()]; ok {
		return t, nil
	}
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName())
	if err != nil {
		return goType{}, fmt.Errorf("payload-limits: no Go type registered for %s: %w", md.FullName(), err)
	}
	ptr := reflect.TypeOf(mt.Zero().Interface())
	elem := ptr.Elem()
	pkgName, _, _ := strings.Cut(elem.String(), ".")
	t := goType{pkgPath: elem.PkgPath(), alias: pkgName + "pb", name: elem.Name(), ptr: ptr}
	if other, ok := e.aliases[t.alias]; ok && other != t.pkgPath {
		return goType{}, fmt.Errorf("payload-limits: import alias %s used by both %s and %s", t.alias, other, t.pkgPath)
	}
	e.aliases[t.alias] = t.pkgPath
	e.imports[t.pkgPath] = t.alias
	e.types[md.FullName()] = t
	return t, nil
}

func (e *emitter) funcName(md protoreflect.MessageDescriptor) (string, error) {
	t, err := e.goType(md)
	if err != nil {
		return "", err
	}
	pkg := strings.TrimSuffix(t.alias, "pb")
	return "check" + strings.ToUpper(pkg[:1]) + pkg[1:] + t.name, nil
}

// getter is the generated Go getter for f, verified to exist so naming drift fails generation
// rather than producing code that doesn't compile.
func (e *emitter) getter(md protoreflect.MessageDescriptor, f protoreflect.FieldDescriptor) (string, error) {
	t, err := e.goType(md)
	if err != nil {
		return "", err
	}
	name := "Get" + goCamelCase(string(f.Name()))
	if _, ok := t.ptr.MethodByName(name); !ok {
		return "", fmt.Errorf("payload-limits: %s.%s has no getter %s", t.name, f.Name(), name)
	}
	return name, nil
}

func (g *generator) emit(toGenerate map[protoreflect.FullName]protoreflect.MessageDescriptor, roots []protoreflect.MessageDescriptor) ([]byte, error) {
	body := &emitter{types: map[protoreflect.FullName]goType{}, imports: map[string]string{}, aliases: map[string]string{}}

	var rootList []protoreflect.MessageDescriptor
	seen := map[protoreflect.FullName]bool{}
	for _, r := range roots {
		if toGenerate[r.FullName()] != nil && !seen[r.FullName()] {
			seen[r.FullName()] = true
			rootList = append(rootList, r)
		}
	}
	slices.SortFunc(rootList, func(a, b protoreflect.MessageDescriptor) int {
		return strings.Compare(string(a.FullName()), string(b.FullName()))
	})

	body.printf("func dispatch(sink Sink, req proto.Message) {\n")
	body.printf("switch r := req.(type) {\n")
	for _, r := range rootList {
		t, err := body.goType(r)
		if err != nil {
			return nil, err
		}
		fn, err := body.funcName(r)
		if err != nil {
			return nil, err
		}
		body.printf("case *%s.%s:\n%s(sink, r)\n", t.alias, t.name, fn)
	}
	body.printf("}\n}\n\n")

	usesSortedMap := false
	for _, name := range sortedKeys(toGenerate) {
		md := toGenerate[protoreflect.FullName(name)]
		sorted, err := g.emitCheckFunc(body, md)
		if err != nil {
			return nil, err
		}
		usesSortedMap = usesSortedMap || sorted
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by payload-limit-gen; DO NOT EDIT.\n\npackage payloadlimits\n\nimport (\n")
	if usesSortedMap {
		fmt.Fprintf(&out, "\"maps\"\n\"slices\"\n\n")
	}
	paths := sortedKeys(body.imports)
	for _, p := range paths {
		fmt.Fprintf(&out, "%s %q\n", body.imports[p], p)
	}
	fmt.Fprintf(&out, "\"google.golang.org/protobuf/proto\"\n)\n\n")
	out.Write(body.buf.Bytes())

	src, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("payload-limits: formatting generated source: %w\n%s", err, out.String())
	}
	return src, nil
}

var classTokens = map[limitClass]string{classBlob: "LimitClassBlob", classMemo: "LimitClassMemo"}

func (g *generator) emitCheckFunc(e *emitter, md protoreflect.MessageDescriptor) (usesSortedMap bool, err error) {
	t, err := e.goType(md)
	if err != nil {
		return false, err
	}
	fn, err := e.funcName(md)
	if err != nil {
		return false, err
	}
	e.printf("func %s(sink Sink, msg *%s.%s) {\n", fn, t.alias, t.name)
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		tg := classify(f)
		switch {
		case tg.isLeaf:
			if err := g.emitLeaf(e, md, f, tg.leaf); err != nil {
				return false, err
			}
		case tg.shape != 0 && g.included(tg.child) && g.validating[tg.child.FullName()]:
			if err := g.emitStruct(e, md, f, tg); err != nil {
				return false, err
			}
			usesSortedMap = usesSortedMap || tg.shape == shapeMap
		}
	}
	e.printf("}\n\n")
	return usesSortedMap, nil
}

func (g *generator) emitLeaf(e *emitter, md protoreflect.MessageDescriptor, f protoreflect.FieldDescriptor, kind leafKind) error {
	p := g.table[leafKey(md, f)]
	if !p.validated {
		return nil
	}
	if kind == leafMemo && p.class == classBlob {
		kind = leafMemoDataSum
	}
	get, err := e.getter(md, f)
	if err != nil {
		return err
	}
	check := func(sizeExpr string) {
		e.printf("sink.Check(%q, %s, %s, %t)\n", f.Name(), classTokens[p.class], sizeExpr, p.enforceError)
	}
	var single string
	switch kind {
	case leafPayloads, leafPayload, leafMemo, leafWholeMessage:
		single = "messageSize(v)"
	case leafMemoDataSum, leafHeader:
		single = "mapPayloadDataSum(v.GetFields())"
	case leafSearchAttributes:
		single = "mapPayloadDataSum(v.GetIndexedFields())"
	case leafRepeatedPayload, leafRepeatedWholeMessage:
		check(fmt.Sprintf("messageSizeSum(msg.%s())", get))
		return nil
	case leafMapPayload:
		check(fmt.Sprintf("mapPayloadDataSum(msg.%s())", get))
		return nil
	case leafMapPayloads:
		check(fmt.Sprintf("mapPayloadsSum(msg.%s())", get))
		return nil
	default:
		return fmt.Errorf("payload-limits: unhandled leaf kind %d for %s", kind, leafKey(md, f))
	}
	e.printf("if v := msg.%s(); v != nil {\n", get)
	check(single)
	e.printf("}\n")
	return nil
}

func (g *generator) emitStruct(e *emitter, md protoreflect.MessageDescriptor, f protoreflect.FieldDescriptor, tg target) error {
	get, err := e.getter(md, f)
	if err != nil {
		return err
	}
	fn, err := e.funcName(tg.child)
	if err != nil {
		return err
	}
	switch tg.shape {
	case shapeSingle:
		e.printf("if v := msg.%s(); v != nil {\nsink.Enter(%q)\n%s(sink, v)\nsink.Exit()\n}\n", get, f.Name(), fn)
	case shapeRepeated:
		e.printf("for i, v := range msg.%s() {\nsink.EnterIndex(%q, i)\n%s(sink, v)\nsink.Exit()\n}\n", get, f.Name(), fn)
	case shapeMap:
		// Sorted so the reported violations, and which comes first, are deterministic.
		e.printf("if m := msg.%s(); len(m) > 0 {\nfor _, k := range slices.Sorted(maps.Keys(m)) {\n"+
			"sink.EnterKey(%q, k)\n%s(sink, m[k])\nsink.Exit()\n}\n}\n", get, f.Name(), fn)
	}
	return nil
}

func sortedKeys[K ~string, V any](m map[K]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

// goCamelCase mirrors protoc-gen-go's field naming (internal/strs.GoCamelCase in
// google.golang.org/protobuf).
func goCamelCase(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '.' && i+1 < len(s) && isASCIILower(s[i+1]):
		case c == '.':
			b = append(b, '_')
		case c == '_' && (i == 0 || s[i-1] == '.'):
			b = append(b, 'X')
		case c == '_' && i+1 < len(s) && isASCIILower(s[i+1]):
		case isASCIIDigit(c):
			b = append(b, c)
		default:
			if isASCIILower(c) {
				c -= 'a' - 'A'
			}
			b = append(b, c)
			for ; i+1 < len(s) && isASCIILower(s[i+1]); i++ {
				b = append(b, s[i+1])
			}
		}
	}
	return string(b)
}

func isASCIILower(c byte) bool { return 'a' <= c && c <= 'z' }
func isASCIIDigit(c byte) bool { return '0' <= c && c <= '9' }
