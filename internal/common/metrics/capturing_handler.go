// The MIT License
//
// Copyright (c) 2021 Temporal Technologies Inc.  All rights reserved.
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

package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// This file contains test helpers only. They are not private because they are used by other tests.

type capturedInfo struct {
	sliceLock sync.RWMutex // Only governs slice access, not what's in the slice
	counters  []*CapturedCounter
	gauges    []*CapturedGauge
	timers    []*CapturedTimer
}

type CapturingHandler struct {
	*capturedInfo
	// Never changed once created
	tags map[string]string
}

var _ Handler = &CapturingHandler{}

func NewCapturingHandler() *CapturingHandler { return &CapturingHandler{capturedInfo: &capturedInfo{}} }

func (c *CapturingHandler) WithTags(tags map[string]string) Handler {
	ret := &CapturingHandler{capturedInfo: c.capturedInfo, tags: make(map[string]string)}
	for k, v := range c.tags {
		ret.tags[k] = v
	}
	for k, v := range tags {
		ret.tags[k] = v
	}
	return ret
}

func (c *CapturingHandler) Counter(name string) Counter {
	ret := &CapturedCounter{CapturedMetricMeta: CapturedMetricMeta{Name: name, Tags: c.tags}}
	c.sliceLock.Lock()
	defer c.sliceLock.Unlock()
	c.counters = append(c.counters, ret)
	return ret
}

func (c *CapturingHandler) Counters() []*CapturedCounter {
	c.sliceLock.RLock()
	defer c.sliceLock.RUnlock()
	ret := make([]*CapturedCounter, len(c.counters))
	copy(ret, c.counters)
	return ret
}

func (c *CapturingHandler) Gauge(name string) Gauge {
	ret := &CapturedGauge{CapturedMetricMeta: CapturedMetricMeta{Name: name, Tags: c.tags}}
	c.sliceLock.Lock()
	defer c.sliceLock.Unlock()
	c.gauges = append(c.gauges, ret)
	return ret
}

func (c *CapturingHandler) Gauges() []*CapturedGauge {
	c.sliceLock.RLock()
	defer c.sliceLock.RUnlock()
	ret := make([]*CapturedGauge, len(c.gauges))
	copy(ret, c.gauges)
	return ret
}

func (c *CapturingHandler) Timer(name string) Timer {
	ret := &CapturedTimer{CapturedMetricMeta: CapturedMetricMeta{Name: name, Tags: c.tags}}
	c.sliceLock.Lock()
	defer c.sliceLock.Unlock()
	c.timers = append(c.timers, ret)
	return ret
}

func (c *CapturingHandler) Timers() []*CapturedTimer {
	c.sliceLock.RLock()
	defer c.sliceLock.RUnlock()
	ret := make([]*CapturedTimer, len(c.timers))
	copy(ret, c.timers)
	return ret
}

type CapturedMetricMeta struct {
	Name string
	Tags map[string]string
}

type CapturedCounter struct {
	CapturedMetricMeta
	value int64
}

func (c *CapturedCounter) Inc(d int64)  { atomic.AddInt64(&c.value, d) }
func (c *CapturedCounter) Value() int64 { return atomic.LoadInt64(&c.value) }

type CapturedGauge struct {
	CapturedMetricMeta
	value     float64
	valueLock sync.RWMutex
}

func (c *CapturedGauge) Update(d float64) {
	c.valueLock.Lock()
	defer c.valueLock.Unlock()
	c.value = d
}

func (c *CapturedGauge) Value() float64 {
	c.valueLock.RLock()
	defer c.valueLock.RUnlock()
	return c.value
}

type CapturedTimer struct {
	CapturedMetricMeta
	value int64
}

func (c *CapturedTimer) Record(d time.Duration) { atomic.StoreInt64(&c.value, int64(d)) }
func (c *CapturedTimer) Value() time.Duration   { return time.Duration(atomic.LoadInt64(&c.value)) }
