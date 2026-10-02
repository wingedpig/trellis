// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package serialq runs background jobs one at a time, in submission order.
package serialq

import "sync"

// Queue runs submitted jobs on a background goroutine, strictly in the order
// they were submitted. Callers that submit while holding a lock get jobs that
// complete in that lock's order — which is what keeps an append to a file
// from racing a rewrite of it. The zero value is ready to use.
type Queue struct {
	mu      sync.Mutex
	pending []func()
	running bool
	wg      sync.WaitGroup
}

// Submit queues job to run after every previously submitted job. It never
// blocks on the job itself.
func (q *Queue) Submit(job func()) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.wg.Add(1)
	q.pending = append(q.pending, job)
	if !q.running {
		q.running = true
		go q.drain()
	}
}

// Wait blocks until every job submitted so far has run.
func (q *Queue) Wait() {
	q.wg.Wait()
}

func (q *Queue) drain() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		job := q.pending[0]
		q.pending[0] = nil
		q.pending = q.pending[1:]
		q.mu.Unlock()
		job()
		q.wg.Done()
	}
}
