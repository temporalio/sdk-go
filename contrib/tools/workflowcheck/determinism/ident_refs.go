// The MIT License
//
// Copyright (c) 2022 Temporal Technologies Inc.  All rights reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package determinism

import (
	"flag"
	"strings"
)

// DefaultIdentRefs are the built-in set of known non-deterministic functions
// and vars and overrides for ones that should be treated as deterministic.
var DefaultIdentRefs = IdentRefs{
	"os.Stderr":  true,
	"os.Stdin":   true,
	"os.Stdout":  true,
	"time.Now":   true,
	"time.Sleep": true,
	// We mark these as deterministic since they give so many false positives
	"(reflect.Value).Interface": false,
	"runtime.Caller":            false,
	// We are considering the global pseudorandom as non-deterministic by default
	// since it's global (even if they set a seed), but we allow use of a manually
	// instantiated random instance that may have a localized, fixed seed
	"math/rand.globalRand": true,
	// Even though the global crypto rand reader var can be replaced, it's good
	// to disallow it by default
	"crypto/rand.Reader": true,
}

// IdentRefs is a map of whether the key, as a qualified type or var name, is
// non-determinism (true value means non-deterministic, false means
// deterministic).
type IdentRefs map[string]bool

// Clone copies the map and returns it.
func (i IdentRefs) Clone() IdentRefs {
	ret := make(IdentRefs, len(i))
	for k, v := range i {
		ret[k] = v
	}
	return ret
}

// SetAllStrings sets values based on the given string values. The strings are
// qualified type names and are assumed as "true" (non-deterministic) unless the
// string ends with "=false" which is then treated as false in the map.
func (i IdentRefs) SetAllStrings(refs []string) IdentRefs {
	for _, ref := range refs {
		if strings.HasSuffix(ref, "=false") {
			i[strings.TrimSuffix(ref, "=false")] = false
		} else {
			i[strings.TrimSuffix(ref, "=true")] = true
		}
	}
	return i
}

// SetAll sets the given values on this map and returns this map.
func (i IdentRefs) SetAll(refs IdentRefs) IdentRefs {
	for k, v := range refs {
		i[k] = v
	}
	return i
}

type identRefsFlag struct{ refs IdentRefs }

// NewIdentRefsFlag creates a flag.Value implementation for using
// IdentRefs.SetAllStrings as a CLI flag value.
func NewIdentRefsFlag(refs IdentRefs) flag.Value { return identRefsFlag{refs} }

func (identRefsFlag) String() string { return "<built-in>" }

func (i identRefsFlag) Set(flag string) error {
	i.refs.SetAllStrings(strings.Split(flag, ","))
	return nil
}
