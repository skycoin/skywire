package statutil

import (
	"sync"
	"time"
)

// Duration represents rolling (moving) average of time.Duration
type Duration struct {
	mx sync.Mutex

	last time.Duration
	roll func(time.Duration) time.Duration
}

// Value returns current average Duration
func (d *Duration) Value() time.Duration {
	d.mx.Lock()
	defer d.mx.Unlock()

	return d.last
}

// Add a time.Duration to the Duration. And get new
// average value back
func (d *Duration) Add(dur time.Duration) (avg time.Duration) {
	avg = d.roll(dur)
	return
}

// AddStartTime is Add(time.Now().Sub(tp))
func (d *Duration) AddStartTime(tp time.Time) (avg time.Duration) {
	return d.Add(time.Now().Sub(tp))
}

// NewDuration creates new Duration using
// given amount of samples. It panics if
// the amount is zero or less
func NewDuration(n int) (d *Duration) {

	if n <= 0 {
		panic("number of samples is too small")
	}

	d = new(Duration)

	var (
		bins    = make([]float64, n)
		average float64
		i, c    int
	)

	d.roll = func(dur time.Duration) time.Duration {
		d.mx.Lock()
		defer d.mx.Unlock()

		if c < n {
			c++
		}

		var x = float64(dur)

		average += (x - bins[i]) / float64(c)
		bins[i] = x
		i = (i + 1) % n

		d.last = time.Duration(average)
		return d.last
	}

	return
}

// Float represents rolling (moving) average of float64
type Float struct {
	mx sync.Mutex

	last float64
	roll func(float64) float64
}

// Value returns current average flaot64
func (f *Float) Value() float64 {
	f.mx.Lock()
	defer f.mx.Unlock()

	return f.last
}

// Add a float64 to the Float. And get new
// average value back
func (f *Float) Add(val float64) (avg float64) {
	avg = f.roll(val)
	return
}

// NewFloat creates new Float using
// given amount of samples. It panics if
// the amount is zero or less
func NewFloat(n int) (f *Float) {

	if n <= 0 {
		panic("number of samples is too small")
	}

	f = new(Float)

	var (
		bins    = make([]float64, n)
		average float64
		i, c    int
	)

	f.roll = func(dur float64) float64 {
		f.mx.Lock()
		defer f.mx.Unlock()

		if c < n {
			c++
		}

		var x = float64(dur)

		average += (x - bins[i]) / float64(c)
		bins[i] = x
		i = (i + 1) % n

		f.last = average
		return average
	}

	return
}
