// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package serialq

import "testing"

func TestQueueRunsInOrder(t *testing.T) {
	var q Queue
	var got []int
	for i := 0; i < 1000; i++ {
		i := i
		q.Submit(func() { got = append(got, i) })
	}
	q.Wait()
	if len(got) != 1000 {
		t.Fatalf("ran %d jobs, want 1000", len(got))
	}
	for i, v := range got {
		if v != i {
			t.Fatalf("job %d ran at position %d", v, i)
		}
	}
}
