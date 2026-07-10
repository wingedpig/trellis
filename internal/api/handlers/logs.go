// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/wingedpig/trellis/internal/logs"
)

// readHistoricalBefore returns up to `limit` of the newest historical
// entries with timestamp strictly before `beforeTime`. Used by the WS
// load_more handler when the in-memory ring buffer is exhausted.
//
// We try progressively wider time windows ending at `beforeTime`, stopping
// as soon as we have at least `limit` entries OR we've exceeded the longest
// lookback horizon. Two reasons for the progressive approach:
//
//   1. SSH/file sources sometimes build per-day grep patterns from the
//      start/end range — a window starting at the zero time would generate
//      a regex of thousands of date prefixes and blow past ARG_MAX.
//   2. For dense logs, the most recent hour usually has plenty of entries
//      and we can avoid scanning the whole history.
//
// StreamHistoricalEntries emits in chronological order with no built-in
// "from the end" knob, so we keep a sliding window of size `limit` and
// drop older entries as newer (but still < beforeTime) ones arrive.
func readHistoricalBefore(ctx context.Context, viewer *logs.Viewer, beforeTime time.Time, limit int, filter *logs.Filter) []logs.LogEntry {
	if limit <= 0 || beforeTime.IsZero() {
		return nil
	}
	// Widen the window each pass; bounded so the SSH date-pattern regex
	// stays well under any plausible ARG_MAX. Final pass at ~3 years.
	windows := []time.Duration{
		1 * time.Hour,
		24 * time.Hour,
		7 * 24 * time.Hour,
		30 * 24 * time.Hour,
		365 * 24 * time.Hour,
		3 * 365 * 24 * time.Hour,
	}
	var window []logs.LogEntry
	for _, lookback := range windows {
		if err := ctx.Err(); err != nil {
			break
		}
		start := beforeTime.Add(-lookback)
		window = window[:0]
		_ = viewer.StreamHistoricalEntries(ctx, start, beforeTime, filter, 0, "", 0, 0, func(e logs.LogEntry) error {
			if !e.Timestamp.Before(beforeTime) {
				return nil
			}
			if len(window) < limit {
				window = append(window, e)
			} else {
				copy(window, window[1:])
				window[limit-1] = e
			}
			return nil
		})
		if len(window) >= limit {
			break
		}
	}
	if len(window) == 0 {
		return nil
	}
	out := make([]logs.LogEntry, len(window))
	copy(out, window)
	return out
}

// isConnectionClosed checks if an error indicates a normal connection close
// (broken pipe, connection reset, etc.) that shouldn't be logged as an error.
func isConnectionClosed(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "use of closed network connection")
}

// Error code for log-related errors.
const ErrLogViewerError = "LOG_VIEWER_ERROR"

// LogHandler handles log viewer API requests.
type LogHandler struct {
	upgraderHolder
	manager *logs.Manager
}

// NewLogHandler creates a new log handler.
func NewLogHandler(manager *logs.Manager) *LogHandler {
	return &LogHandler{manager: manager}
}

// List returns all log viewers and their status.
func (h *LogHandler) List(w http.ResponseWriter, r *http.Request) {
	statuses := h.manager.ListStatus()
	WriteJSON(w, http.StatusOK, statuses)
}

// Get returns a single log viewer's status.
func (h *LogHandler) Get(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, ok := h.manager.Get(name)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "log viewer not found")
		return
	}

	WriteJSON(w, http.StatusOK, viewer.Status())
}

// GetEntries returns filtered log entries.
func (h *LogHandler) GetEntries(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, err := h.manager.GetAndStart(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	// Parse query parameters
	query := r.URL.Query()
	filterStr := query.Get("filter")
	limitStr := query.Get("limit")
	beforeStr := query.Get("before")
	afterStr := query.Get("after")

	// Parse filter
	var filter *logs.Filter
	if filterStr != "" {
		var err error
		filter, err = logs.ParseFilter(filterStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid filter: "+err.Error())
			return
		}
	}

	// Parse limit
	limit := 1000 // Default limit
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Parse time range
	var before, after time.Time
	if beforeStr != "" {
		t, err := time.Parse(time.RFC3339, beforeStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid 'before' timestamp: expected RFC3339 format")
			return
		}
		before = t
	}
	if afterStr != "" {
		t, err := time.Parse(time.RFC3339, afterStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid 'after' timestamp: expected RFC3339 format")
			return
		}
		after = t
	}

	// Get entries
	var entries []logs.LogEntry
	if !before.IsZero() || !after.IsZero() {
		if after.IsZero() {
			after = time.Time{}
		}
		if before.IsZero() {
			before = time.Now()
		}

		if filter != nil {
			// When filtering with time range, get all entries in range first,
			// then filter, then apply limit to ensure we return up to limit matches
			entries = viewer.GetEntriesRange(after, before, 0) // 0 = no limit
			var filtered []logs.LogEntry
			for _, e := range entries {
				if filter.Match(e) {
					filtered = append(filtered, e)
					if limit > 0 && len(filtered) >= limit {
						break
					}
				}
			}
			entries = filtered
		} else {
			// No filter, apply limit directly
			entries = viewer.GetEntriesRange(after, before, limit)
		}
	} else {
		entries = viewer.GetEntries(filter, limit)
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"entries":  entries,
		"count":    len(entries),
		"sequence": viewer.CurrentSequence(),
	})
}

// Default deadline for historical log queries. Scanning rotated files plus
// applying grep/regex over a wide time range can take many seconds; the old
// 60s ceiling was too tight for production-sized log volumes. Override per
// request with ?timeout=<duration>.
const defaultHistoryTimeout = 5 * time.Minute

// Maximum allowed value for the per-request timeout query parameter, to keep
// a misbehaving client from pinning a server goroutine indefinitely.
const maxHistoryTimeout = 30 * time.Minute

// historyHeartbeatInterval is the cadence at which streamed history responses
// emit a no-op JSON object so the client knows the connection is still alive
// even when matches are sparse.
const historyHeartbeatInterval = 10 * time.Second

// GetHistory returns historical log entries from rotated files.
//
// If the client sends Accept: application/x-ndjson, the response is streamed
// as newline-delimited JSON: one LogEntry per line, periodic heartbeat lines
// of {"_heartbeat":true}, and on mid-stream failure a final {"_error":"..."}
// line. Otherwise the legacy buffered JSON envelope is returned.
func (h *LogHandler) GetHistory(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// History reads come from rotated files on disk; they don't need the
	// tail running (and shouldn't start one for explore-mode viewers).
	viewer, ok := h.manager.Get(name)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "log viewer not found")
		return
	}
	viewer.Touch()

	// Parse query parameters
	query := r.URL.Query()
	startStr := query.Get("start")
	endStr := query.Get("end")
	filterStr := query.Get("filter")
	limitStr := query.Get("limit")
	grepStr := query.Get("grep")
	beforeStr := query.Get("before")
	afterStr := query.Get("after")
	timeoutStr := query.Get("timeout")

	// Parse time range (required)
	start, err := time.Parse(time.RFC3339, startStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid start time")
		return
	}
	end, err := time.Parse(time.RFC3339, endStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid end time")
		return
	}

	// Parse filter
	var filter *logs.Filter
	if filterStr != "" {
		filter, err = logs.ParseFilter(filterStr)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrBadRequest, "invalid filter: "+err.Error())
			return
		}
	}

	// Parse limit
	limit := 10000 // Higher default for historical queries
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	// Parse context lines (grep -B/-A)
	var grepBefore, grepAfter int
	if beforeStr != "" {
		grepBefore, _ = strconv.Atoi(beforeStr)
	}
	if afterStr != "" {
		grepAfter, _ = strconv.Atoi(afterStr)
	}

	// Resolve per-request timeout
	timeout := defaultHistoryTimeout
	if timeoutStr != "" {
		if d, err := time.ParseDuration(timeoutStr); err == nil && d > 0 {
			if d > maxHistoryTimeout {
				d = maxHistoryTimeout
			}
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if acceptsNDJSON(r) {
		h.streamHistory(ctx, w, viewer, start, end, filter, limit, grepStr, grepBefore, grepAfter)
		return
	}

	entries, err := viewer.GetHistoricalEntries(ctx, start, end, filter, limit, grepStr, grepBefore, grepAfter)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrLogViewerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"entries": entries,
		"count":   len(entries),
		"start":   start,
		"end":     end,
	})
}

// acceptsNDJSON returns true if the client signalled a preference for
// newline-delimited JSON via the Accept header.
func acceptsNDJSON(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept") {
		for _, part := range strings.Split(v, ",") {
			mt := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
			if strings.EqualFold(mt, "application/x-ndjson") || strings.EqualFold(mt, "application/ndjson") {
				return true
			}
		}
	}
	return false
}

// streamHistory writes the historical entries as NDJSON, flushing after every
// line. Headers are sent immediately so the client never sits in
// "awaiting headers" while the disk scan runs.
func (h *LogHandler) streamHistory(ctx context.Context, w http.ResponseWriter, viewer *logs.Viewer, start, end time.Time, filter *logs.Filter, limit int, grep string, grepBefore, grepAfter int) {
	flusher, _ := w.(http.Flusher)

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}

	// Heartbeats are guarded by writeMu — both the producer and the heartbeat
	// goroutine call writeLine concurrently.
	var writeMu sync.Mutex
	enc := json.NewEncoder(w)
	writeLine := func(v interface{}) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := enc.Encode(v); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}

	// Heartbeat ticker. We stop it before returning so the goroutine exits.
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go func() {
		t := time.NewTicker(historyHeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-t.C:
				if err := writeLine(map[string]interface{}{"_heartbeat": true}); err != nil {
					return // client gone
				}
			}
		}
	}()

	streamErr := viewer.StreamHistoricalEntries(ctx, start, end, filter, limit, grep, grepBefore, grepAfter, func(e logs.LogEntry) error {
		return writeLine(e)
	})

	stopHeartbeat()

	if streamErr != nil && !isConnectionClosed(streamErr) {
		// We've already written headers, so we can't change the status code.
		// Surface the error as a final NDJSON line; the client checks for it.
		_ = writeLine(map[string]interface{}{"_error": streamErr.Error()})
		log.Printf("streamHistory: %v", streamErr)
	}
}

// ListRotatedFiles returns available rotated log files.
func (h *LogHandler) ListRotatedFiles(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	// Rotated-file listing reads from disk; it doesn't need the tail
	// running (and shouldn't start one for explore-mode viewers).
	viewer, ok := h.manager.Get(name)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "log viewer not found")
		return
	}
	viewer.Touch()

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	files, err := viewer.ListRotatedFiles(ctx)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrLogViewerError, err.Error())
		return
	}

	WriteJSON(w, http.StatusOK, files)
}

// Stream handles WebSocket connections for streaming log entries.
// Stream protocol tuning knobs.
const (
	// streamFlushInterval is how often batched entries are flushed to the
	// client. Batching turns per-line WebSocket frames into a few frames
	// per second regardless of log volume.
	streamFlushInterval = 150 * time.Millisecond
	// streamMaxBatch flushes early when this many entries are pending.
	streamMaxBatch = 500
	// streamStatsInterval is how often a paused connection receives a
	// lightweight stats frame (missed count + rate) instead of entries.
	streamStatsInterval = 2 * time.Second
	// streamMaxReplay caps how many entries are replayed when a paused
	// stream resumes. Anything older is dropped and reported via "dropped".
	streamMaxReplay = 2000
	// streamExploreInitial is how many recent entries an explore-mode
	// connection loads up front via the backward reader.
	streamExploreInitial = 200
)

// streamCtrl is a control message passed from the WebSocket read goroutine
// to the streaming loop.
type streamCtrl struct {
	kind     string // "pause" or "resume"
	afterSeq uint64 // resume: replay entries after this sequence
}

func (h *LogHandler) Stream(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, ok := h.manager.Get(name)
	if !ok {
		WriteError(w, http.StatusNotFound, ErrNotFound, "log viewer not found")
		return
	}

	// Explore mode: don't start the tail; serve recent entries from the
	// backward reader and only go live when the client asks. Sources
	// without byte-offset backward reads (docker/k8s/command) fall back to
	// live-but-paused, which does start the tail.
	explore := viewer.Config().GetMode() == "explore" && viewer.CanReadBackward()
	if !explore {
		if _, err := h.manager.GetAndStart(name); err != nil {
			WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
			return
		}
	}

	// Upgrade to WebSocket
	conn, err := h.ws().Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Log stream: upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	// Parse initial filter from query params
	filterStr := r.URL.Query().Get("filter")
	var filter *logs.Filter
	var filterMu sync.RWMutex
	if filterStr != "" {
		var err error
		filter, err = logs.ParseFilter(filterStr)
		if err != nil {
			// Send error but continue with no filter
			conn.WriteJSON(map[string]interface{}{
				"type":  "error",
				"error": "invalid filter: " + err.Error(),
			})
		}
	}

	// Channel for receiving entries. In explore mode the subscription is
	// deferred until the client goes live; Unsubscribe on a channel that was
	// never subscribed is a no-op.
	entryCh := make(chan logs.LogEntry, 1000)
	subscribed := false
	if !explore {
		viewer.Subscribe(entryCh)
		subscribed = true
	}
	defer viewer.Unsubscribe(entryCh)

	// Whether the viewer's config asks for explore-style UI (even when the
	// source forced the live-paused fallback).
	exploreUI := viewer.Config().GetMode() == "explore"

	// Send connection status
	conn.WriteJSON(map[string]interface{}{
		"type":      "status",
		"viewer":    name,
		"connected": true,
		"mode":      viewer.Config().GetMode(),
		"live":      !exploreUI,
		"sequence":  viewer.CurrentSequence(),
	})

	// lastSentSeq tracks the newest sequence handled while streaming; the
	// stream loop skips anything at or below it so a resume replay can never
	// duplicate entries already queued in entryCh.
	var lastSentSeq uint64

	// Send initial entries. reason:"init" tells the client to render them
	// regardless of its follow state.
	if explore {
		initCtx, initCancel := context.WithTimeout(r.Context(), 30*time.Second)
		entries, cursor, _, _, err := viewer.ReadEntriesBackward(initCtx, logs.BackwardCursor{Offset: -1}, streamExploreInitial, filter, time.Time{})
		initCancel()
		if err != nil {
			log.Printf("logs[%s]: explore initial read failed: %v", name, err)
		}
		conn.WriteJSON(map[string]interface{}{
			"type":        "entries",
			"reason":      "init",
			"entries":     entries,
			"next_cursor": cursor,
		})
	} else {
		initialEntries := viewer.GetEntries(filter, 100)
		if len(initialEntries) > 0 {
			conn.WriteJSON(map[string]interface{}{
				"type":    "entries",
				"reason":  "init",
				"entries": initialEntries,
			})
			for _, e := range initialEntries {
				if e.Sequence > lastSentSeq {
					lastSentSeq = e.Sequence
				}
			}
		}
	}

	// Paused streams drop entries after counting them; resume replays from
	// the ring buffer. Explore connections start paused.
	paused := exploreUI

	// Control channel from the read goroutine to the stream loop. Pause and
	// resume must be handled by the loop that owns the streaming state,
	// otherwise replay and live sends race and the client sees duplicates.
	ctrlCh := make(chan streamCtrl, 8)

	// Closed when Stream returns, so the read goroutine never blocks on
	// ctrlCh after the stream loop has exited.
	loopExit := make(chan struct{})
	defer close(loopExit)

	// Mutex for WebSocket writes
	var writeMu sync.Mutex

	// Set up ping/pong
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Done channel
	done := make(chan struct{})

	// Ping goroutine
	go func() {
		ticker := time.NewTicker(54 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writeMu.Lock()
				err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				writeMu.Unlock()
				if err != nil {
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Read goroutine for client messages and disconnect detection
	go func() {
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				// Normal close - don't log as error
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) &&
					!websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
					// Only log unexpected errors (not normal disconnects or broken pipes)
					if !isConnectionClosed(err) {
						log.Printf("Log stream %s: read error: %v", name, err)
					}
				}
				close(done)
				return
			}

			// Parse client message
			var clientMsg struct {
				Type          string               `json:"type"`
				Query         string               `json:"query"`
				SeekTS        string               `json:"timestamp"`
				AfterSeq      uint64               `json:"after_seq"`
				BeforeSeq     uint64               `json:"before_seq"`
				BeforeTime    string               `json:"before_time"`
				HistoryCursor *logs.BackwardCursor `json:"history_cursor,omitempty"`
				Limit         int                  `json:"limit"`
			}
			if err := json.Unmarshal(msg, &clientMsg); err != nil {
				continue
			}

			switch clientMsg.Type {
			case "filter":
				// Update filter
				filterMu.Lock()
				if clientMsg.Query != "" {
					newFilter, err := logs.ParseFilter(clientMsg.Query)
					if err != nil {
						filterMu.Unlock()
						// Send error back to client
						writeMu.Lock()
						conn.WriteJSON(map[string]interface{}{
							"type":  "error",
							"error": "invalid filter: " + err.Error(),
						})
						writeMu.Unlock()
					} else {
						filter = newFilter
						filterMu.Unlock()
					}
				} else {
					filter = nil
					filterMu.Unlock()
				}
			case "pause":
				// Client wants to pause streaming; handled by the stream
				// loop, which owns the pause/replay state.
				select {
				case ctrlCh <- streamCtrl{kind: "pause"}:
				case <-loopExit:
					return
				}
			case "resume":
				// Client wants to resume streaming, replaying entries newer
				// than after_seq from the ring buffer.
				select {
				case ctrlCh <- streamCtrl{kind: "resume", afterSeq: clientMsg.AfterSeq}:
				case <-loopExit:
					return
				}
			case "load_more":
				// Client wants older entries (scrolled to top). Three-stage
				// fallback:
				//   1. In-memory ring buffer (fast, sequence-keyed).
				//   2. Byte-offset backward reader (BackwardReader). For
				//      file/SSH sources this paginates large histories
				//      cheaply via stat + tail|head.
				//   3. Time-window historical reader. Used for source
				//      types without a seekable byte stream (Docker, K8s,
				//      Command) AND as a fallback when the byte-offset
				//      reader runs out of *uncompressed* files — those it
				//      skips, but time-window reads them via decompress.
				limit := clientMsg.Limit
				if limit <= 0 || limit > 100 {
					limit = 100
				}

				olderEntries := viewer.GetEntriesBefore(clientMsg.BeforeSeq, limit)
				source := "memory"
				var nextCursor *logs.BackwardCursor
				byteOffsetDone := false

				if len(olderEntries) == 0 {
					filterMu.RLock()
					f := filter
					filterMu.RUnlock()
					hCtx, hCancel := context.WithTimeout(context.Background(), 30*time.Second)

					// Parse before_time once — needed both for the byte-
					// offset seek and the time-window fallback below.
					var beforeTime time.Time
					if clientMsg.BeforeTime != "" {
						if t, perr := time.Parse(time.RFC3339Nano, clientMsg.BeforeTime); perr == nil {
							beforeTime = t
						} else if t, perr := time.Parse(time.RFC3339, clientMsg.BeforeTime); perr == nil {
							beforeTime = t
						}
					}

					// Stage 2: byte-offset reader if the source supports it.
					cur := logs.BackwardCursor{Offset: -1}
					if clientMsg.HistoryCursor != nil {
						cur = *clientMsg.HistoryCursor
					}
					entries, c, d, skippedCompressed, err := viewer.ReadEntriesBackward(hCtx, cur, limit, f, beforeTime)
					if err == nil {
						olderEntries = entries
						nc := c
						nextCursor = &nc
						byteOffsetDone = d
						source = "history"
					} else {
						log.Printf("logs[%s]: byte-offset reader failed at cursor %+v: %v", name, cur, err)
					}

					// Stage 3: time-window fallback. Only when there's
					// reason to believe time-window can find content
					// byte-offset missed — i.e. the byte-offset reader
					// skipped one or more compressed (.gz) rotated files.
					// Without compressed files, byte-offset has read
					// everything the source can offer; running time-
					// window's date-pattern grep against the same files
					// would just re-find the same content (or nothing).
					if len(olderEntries) == 0 && skippedCompressed && !beforeTime.IsZero() {
						olderEntries = readHistoricalBefore(hCtx, viewer, beforeTime, limit, f)
						if len(olderEntries) > 0 {
							source = "history-timewindow"
						}
					}
					hCancel()
				}

				// Build response.
				//
				// no_more is set only when we genuinely have no more
				// history to offer: byte-offset is done AND time-window
				// found nothing. Either of those returning entries means
				// the client should keep scrolling.
				payload := map[string]interface{}{
					"type":    "older_entries",
					"entries": olderEntries,
				}
				if len(olderEntries) > 0 {
					payload["source"] = source
				}
				if nextCursor != nil {
					payload["next_cursor"] = nextCursor
				}
				if len(olderEntries) == 0 && byteOffsetDone {
					payload["no_more"] = true
				}
				writeMu.Lock()
				conn.WriteJSON(payload)
				writeMu.Unlock()
			}
		}
	}()

	// Main loop - stream entries to client in batches. Entries are
	// coalesced and flushed every streamFlushInterval (or when the batch
	// hits streamMaxBatch) so high-volume logs cost a few frames per second
	// instead of a frame per line. While paused, matching entries are
	// counted and dropped; a periodic stats frame keeps the client's "new
	// lines" counter fresh without shipping the lines.
	var pending []logs.LogEntry
	pausedCount := 0   // matched entries dropped since pause
	windowMatched := 0 // matched entries seen in the current stats window

	flush := func() bool {
		if len(pending) == 0 {
			return true
		}
		writeMu.Lock()
		err := conn.WriteJSON(map[string]interface{}{
			"type":    "entries",
			"entries": pending,
		})
		writeMu.Unlock()
		pending = pending[:0]
		if err != nil {
			log.Printf("Log stream: write failed: %v", err)
			return false
		}
		return true
	}

	flushTicker := time.NewTicker(streamFlushInterval)
	defer flushTicker.Stop()
	statsTicker := time.NewTicker(streamStatsInterval)
	defer statsTicker.Stop()

	for {
		select {
		case entry, ok := <-entryCh:
			if !ok {
				log.Printf("Log stream %s: entry channel closed", name)
				return
			}

			// Apply filter
			filterMu.RLock()
			currentFilter := filter
			filterMu.RUnlock()
			if currentFilter != nil && !currentFilter.Match(entry) {
				if !paused && entry.Sequence > lastSentSeq {
					lastSentSeq = entry.Sequence
				}
				continue
			}
			windowMatched++

			// While paused, count and drop; the ring buffer keeps the
			// entries for replay on resume.
			if paused {
				pausedCount++
				continue
			}

			// Skip anything a replay already covered.
			if entry.Sequence <= lastSentSeq {
				continue
			}
			lastSentSeq = entry.Sequence

			pending = append(pending, entry)
			if len(pending) >= streamMaxBatch {
				if !flush() {
					return
				}
			}

		case <-flushTicker.C:
			if !flush() {
				return
			}

		case <-statsTicker.C:
			// A config reload replaces viewer objects in the manager. This
			// connection would then be subscribed to the orphaned old viewer
			// and stream nothing, forever. Close instead — the client
			// reconnects within seconds and binds to the replacement (its
			// status message carries the new sequence epoch).
			if current, ok := h.manager.Get(name); !ok || current != viewer {
				log.Printf("Log stream %s: viewer replaced or removed; closing stream for reconnect", name)
				return
			}

			rate := float64(windowMatched) / streamStatsInterval.Seconds()
			windowMatched = 0
			if !paused || !subscribed {
				continue
			}
			writeMu.Lock()
			err := conn.WriteJSON(map[string]interface{}{
				"type":      "stats",
				"new_count": pausedCount,
				"rate":      rate,
			})
			writeMu.Unlock()
			if err != nil {
				log.Printf("Log stream: stats write failed: %v", err)
				return
			}

		case c := <-ctrlCh:
			switch c.kind {
			case "pause":
				if !flush() {
					return
				}
				if !paused {
					paused = true
					pausedCount = 0
				}

			case "resume":
				// Going live may need to start the tail (explore mode) and
				// subscribe this connection.
				justSubscribed := false
				if !subscribed {
					if err := h.manager.EnsureStarted(name); err != nil {
						writeMu.Lock()
						conn.WriteJSON(map[string]interface{}{
							"type":  "error",
							"error": "failed to start log viewer: " + err.Error(),
						})
						writeMu.Unlock()
						continue
					}
					viewer.Subscribe(entryCh)
					subscribed = true
					justSubscribed = true
				}

				// Replay what was missed while paused. The replay comes from
				// the ring buffer; entries still queued in entryCh with
				// sequences at or below the replay horizon are skipped by the
				// lastSentSeq check above.
				//
				// A connection that just subscribed with no sequence horizon
				// (explore go-live) still gets a small catch-up: the source
				// may have emitted lines between EnsureStarted and Subscribe
				// that only the ring buffer saw, and if the viewer was
				// already running for another watcher the buffer holds the
				// recent context the cleared client needs.
				afterSeq := c.afterSeq
				if lastSentSeq > afterSeq {
					afterSeq = lastSentSeq
				}
				var replay []logs.LogEntry
				dropped := 0
				var raw []logs.LogEntry
				if afterSeq > 0 {
					raw, dropped = viewer.GetLastEntriesAfter(afterSeq, streamMaxReplay)
				} else if justSubscribed {
					raw, _ = viewer.GetLastEntriesAfter(0, streamExploreInitial)
				}
				if len(raw) > 0 {
					filterMu.RLock()
					currentFilter := filter
					filterMu.RUnlock()
					for _, e := range raw {
						if e.Sequence > lastSentSeq {
							lastSentSeq = e.Sequence
						}
						if currentFilter != nil && !currentFilter.Match(e) {
							continue
						}
						replay = append(replay, e)
					}
				}

				paused = false
				pausedCount = 0

				writeMu.Lock()
				err := conn.WriteJSON(map[string]interface{}{
					"type":    "entries",
					"reason":  "replay",
					"entries": replay,
					"dropped": dropped,
				})
				writeMu.Unlock()
				if err != nil {
					log.Printf("Log stream: replay write failed: %v", err)
					return
				}
			}

		case <-done:
			return
		}
	}
}

// StreamSSE streams log entries via Server-Sent Events for CLI consumption.
func (h *LogHandler) StreamSSE(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	name := vars["name"]

	viewer, err := h.manager.GetAndStart(name)
	if err != nil {
		WriteError(w, http.StatusNotFound, ErrNotFound, err.Error())
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, http.StatusInternalServerError, ErrInternalError, "streaming not supported")
		return
	}

	// Subscribe to entries
	entryCh := make(chan logs.LogEntry, 1000)
	viewer.Subscribe(entryCh)
	defer viewer.Unsubscribe(entryCh)

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"viewer\":%q}\n\n", name)
	flusher.Flush()

	// Set up keepalive ticker
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Stream entries
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			// Send keepalive comment
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case entry, ok := <-entryCh:
			if !ok {
				return
			}
			// Send entry as JSON
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
