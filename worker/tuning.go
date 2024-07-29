// The MIT License
//
// Copyright (c) 2020 Temporal Technologies Inc.  All rights reserved.
//
// Copyright (c) 2020 Uber Technologies, Inc.
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

package worker

import (
	"go.temporal.io/sdk/internal"
)

// WorkerTuner allows for the dynamic customization of some aspects of worker behavior.
type WorkerTuner = internal.WorkerTuner

// SlotPermit is a permit to use a slot.
type SlotPermit = internal.SlotPermit

// SlotSupplier controls how slots are handed out for workflow and activity tasks as well as
// local activities when used in conjunction with a WorkerTuner.
//
// Currently, you cannot implement your own slot supplier. You can use the provided
// FixedSizeSlotSupplier and ResourceBasedSlotSupplier slot suppliers.
type SlotSupplier = internal.SlotSupplier

// SlotReserveContext contains information that SlotSupplier instances can use during
// reservation calls.
type SlotReserveContext = internal.SlotReserveContext

// FixedSizeTunerOptions are the options used by NewFixedSizeTuner.
type FixedSizeTunerOptions = internal.FixedSizeTunerOptions

// CompositeTunerOptions are the options used by NewCompositeTuner.
type CompositeTunerOptions = internal.CompositeTunerOptions

// NewFixedSizeTuner creates a WorkerTuner that uses fixed size slot suppliers.
func NewFixedSizeTuner(opts FixedSizeTunerOptions) (WorkerTuner, error) {
	return internal.NewFixedSizeTuner(opts)
}

// NewCompositeTuner creates a WorkerTuner that uses a combination of slot suppliers.
func NewCompositeTuner(opts CompositeTunerOptions) (WorkerTuner, error) {
	return internal.NewCompositeTuner(opts)
}

// NewFixedSizeSlotSupplier creates a new FixedSizeSlotSupplier with the given number of slots.
func NewFixedSizeSlotSupplier(numSlots int) (SlotSupplier, error) {
	return internal.NewFixedSizeSlotSupplier(numSlots)
}
