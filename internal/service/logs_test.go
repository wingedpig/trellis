// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogBuffer_Write(t *testing.T) {
	buffer := NewLogBuffer(10)

	buffer.Write("line 1")
	buffer.Write("line 2")
	buffer.Write("line 3")

	lines := buffer.Lines(10)
	require.Len(t, lines, 3)
	assert.Equal(t, "line 1", lines[0])
	assert.Equal(t, "line 2", lines[1])
	assert.Equal(t, "line 3", lines[2])
}

func TestLogBuffer_RingBehavior(t *testing.T) {
	buffer := NewLogBuffer(5)

	// Write more lines than capacity
	for i := 1; i <= 10; i++ {
		buffer.Write(string(rune('0' + i)))
	}

	// Should only have last 5 lines
	lines := buffer.Lines(10)
	require.Len(t, lines, 5)

	// Oldest lines should be dropped
	assert.Equal(t, "6", lines[0])
	assert.Equal(t, "7", lines[1])
	assert.Equal(t, "8", lines[2])
	assert.Equal(t, "9", lines[3])
	assert.Equal(t, ":", lines[4]) // rune('0' + 10) = ':'
}

func TestLogBuffer_Lines_Limit(t *testing.T) {
	buffer := NewLogBuffer(10)

	for i := 0; i < 10; i++ {
		buffer.Write(string(rune('a' + i)))
	}

	// Request only last 3 lines
	lines := buffer.Lines(3)
	require.Len(t, lines, 3)
	assert.Equal(t, "h", lines[0])
	assert.Equal(t, "i", lines[1])
	assert.Equal(t, "j", lines[2])
}

func TestLogBuffer_Lines_MoreThanAvailable(t *testing.T) {
	buffer := NewLogBuffer(10)

	buffer.Write("only one")

	// Request more than available
	lines := buffer.Lines(100)
	require.Len(t, lines, 1)
	assert.Equal(t, "only one", lines[0])
}

func TestLogBuffer_Lines_Empty(t *testing.T) {
	buffer := NewLogBuffer(10)

	lines := buffer.Lines(10)
	assert.Empty(t, lines)
}

func TestLogBuffer_Clear(t *testing.T) {
	buffer := NewLogBuffer(10)

	buffer.Write("line 1")
	buffer.Write("line 2")

	buffer.Clear()

	lines := buffer.Lines(10)
	assert.Empty(t, lines)
}

func TestLogBuffer_All(t *testing.T) {
	buffer := NewLogBuffer(5)

	for i := 1; i <= 3; i++ {
		buffer.Write(string(rune('0' + i)))
	}

	lines := buffer.All()
	require.Len(t, lines, 3)
	assert.Equal(t, "1", lines[0])
	assert.Equal(t, "2", lines[1])
	assert.Equal(t, "3", lines[2])
}

func TestLogBuffer_Concurrency(t *testing.T) {
	buffer := NewLogBuffer(1000)

	var wg sync.WaitGroup
	done := make(chan bool)

	// Concurrent writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				buffer.Write("line")
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				buffer.Lines(10)
			}
		}()
	}

	go func() {
		wg.Wait()
		done <- true
	}()

	<-done
}

func TestLogBuffer_WriteMultiline(t *testing.T) {
	buffer := NewLogBuffer(10)

	// Write multiline content - each line should be stored separately
	buffer.WriteLines("line 1\nline 2\nline 3")

	lines := buffer.Lines(10)
	require.Len(t, lines, 3)
	assert.Equal(t, "line 1", lines[0])
	assert.Equal(t, "line 2", lines[1])
	assert.Equal(t, "line 3", lines[2])
}

func TestLogBuffer_WriteBytes(t *testing.T) {
	buffer := NewLogBuffer(10)

	buffer.WriteBytes([]byte("hello\nworld"))

	lines := buffer.Lines(10)
	require.Len(t, lines, 2)
	assert.Equal(t, "hello", lines[0])
	assert.Equal(t, "world", lines[1])
}

func TestLogBuffer_Size(t *testing.T) {
	buffer := NewLogBuffer(10)

	assert.Equal(t, 0, buffer.Size())

	buffer.Write("line 1")
	assert.Equal(t, 1, buffer.Size())

	buffer.Write("line 2")
	assert.Equal(t, 2, buffer.Size())

	buffer.Clear()
	assert.Equal(t, 0, buffer.Size())
}

func TestLogBuffer_Capacity(t *testing.T) {
	buffer := NewLogBuffer(42)
	assert.Equal(t, 42, buffer.Capacity())
}

func TestLogBuffer_ZeroCapacity(t *testing.T) {
	// Zero capacity should use default
	buffer := NewLogBuffer(0)
	assert.Greater(t, buffer.Capacity(), 0)
}

func TestLogBuffer_NegativeCapacity(t *testing.T) {
	// Negative capacity should use default
	buffer := NewLogBuffer(-10)
	assert.Greater(t, buffer.Capacity(), 0)
}

func TestLogBuffer_EmptyLines(t *testing.T) {
	buffer := NewLogBuffer(10)

	buffer.WriteLines("\n\n\n")

	// Empty lines should be stored
	lines := buffer.Lines(10)
	require.Len(t, lines, 3)
	assert.Equal(t, "", lines[0])
	assert.Equal(t, "", lines[1])
	assert.Equal(t, "", lines[2])
}

func TestLogBuffer_TrailingNewline(t *testing.T) {
	buffer := NewLogBuffer(10)

	buffer.WriteLines("line 1\nline 2\n")

	// Trailing newline should not create extra empty line
	lines := buffer.Lines(10)
	require.Len(t, lines, 2)
	assert.Equal(t, "line 1", lines[0])
	assert.Equal(t, "line 2", lines[1])
}
