package common

import (
	"time"
)

func TimeValue(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func DurationValue(d *time.Duration) time.Duration {
	if d == nil {
		return 0
	}
	return *d
}

func MinDurationPtr(d1 *time.Duration, d2 *time.Duration) *time.Duration {
	d1v, d2v := DurationValue(d1), DurationValue(d2)
	if d1v > d2v {
		return &d2v
	}
	return &d1v
}

func DurationPtr(d time.Duration) *time.Duration {
	return &d
}
