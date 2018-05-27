package main

import (
	"time"
)

type debouncer struct {
	inCh, C, stopCh chan struct{}
}

func newDebouncer(duration time.Duration) *debouncer {
	d := &debouncer{
		inCh:   make(chan struct{}),
		C:      make(chan struct{}),
		stopCh: make(chan struct{}),
	}

	go func() {
		<-d.inCh

		timer := time.NewTimer(duration)

		for {
			select {
			case <-timer.C:
				d.C <- struct{}{}
			case <-d.inCh:
				timer.Reset(duration)
			case <-d.stopCh:
				timer.Stop()
				break
			}
		}
	}()

	return d
}

func (d *debouncer) ping() {
	d.inCh <- struct{}{}
}

func (d *debouncer) stop() {
	d.stopCh <- struct{}{}
}
