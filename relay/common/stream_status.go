package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type StreamEndReason string

const (
	StreamEndReasonNone        StreamEndReason = ""
	StreamEndReasonDone        StreamEndReason = "done"
	StreamEndReasonTimeout     StreamEndReason = "timeout"
	StreamEndReasonClientGone  StreamEndReason = "client_gone"
	StreamEndReasonScannerErr  StreamEndReason = "scanner_error"
	StreamEndReasonHandlerStop StreamEndReason = "handler_stop"
	StreamEndReasonEOF         StreamEndReason = "eof"
	StreamEndReasonPanic       StreamEndReason = "panic"
	StreamEndReasonPingFail    StreamEndReason = "ping_fail"
)

type StreamTerminationKind string

const (
	StreamTerminationNormal    StreamTerminationKind = "normal"
	StreamTerminationAbnormal  StreamTerminationKind = "abnormal"
	StreamTerminationCancelled StreamTerminationKind = "cancelled"
)

type StreamTermination struct {
	Kind      StreamTerminationKind
	EndReason StreamEndReason
	EndError  error
}

func (t StreamTermination) IsNormal() bool {
	return t.Kind == StreamTerminationNormal
}

func (t StreamTermination) IsCancelled() bool {
	return t.Kind == StreamTerminationCancelled
}

const maxStreamErrorEntries = 20

type StreamErrorEntry struct {
	Message   string
	Timestamp time.Time
}

type StreamStatus struct {
	EndReason StreamEndReason
	EndError  error
	endOnce   sync.Once

	mu         sync.Mutex
	Errors     []StreamErrorEntry
	ErrorCount int
}

func NewStreamStatus() *StreamStatus {
	return &StreamStatus{}
}

func (s *StreamStatus) SetEndReason(reason StreamEndReason, err error) {
	if s == nil {
		return
	}
	s.endOnce.Do(func() {
		s.EndReason = reason
		s.EndError = err
	})
}

func (s *StreamStatus) RecordError(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ErrorCount++
	if len(s.Errors) < maxStreamErrorEntries {
		s.Errors = append(s.Errors, StreamErrorEntry{
			Message:   msg,
			Timestamp: time.Now(),
		})
	}
}

func (s *StreamStatus) HasErrors() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount > 0
}

func (s *StreamStatus) TotalErrorCount() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ErrorCount
}

func (s *StreamStatus) IsNormalEnd() bool {
	if s == nil {
		return true
	}
	return s.EndReason == StreamEndReasonDone ||
		s.EndReason == StreamEndReasonEOF ||
		s.EndReason == StreamEndReasonHandlerStop
}

func (s *StreamStatus) Termination() StreamTermination {
	if s == nil {
		return StreamTermination{Kind: StreamTerminationAbnormal}
	}
	termination := StreamTermination{
		Kind:      StreamTerminationAbnormal,
		EndReason: s.EndReason,
		EndError:  s.EndError,
	}
	if s.EndReason == StreamEndReasonClientGone {
		termination.Kind = StreamTerminationCancelled
		return termination
	}
	if s.EndReason == StreamEndReasonHandlerStop &&
		(errors.Is(s.EndError, context.Canceled) || errors.Is(s.EndError, context.DeadlineExceeded)) {
		termination.Kind = StreamTerminationCancelled
		return termination
	}
	if (s.EndReason == StreamEndReasonDone || s.EndReason == StreamEndReasonEOF) && !s.HasErrors() {
		termination.Kind = StreamTerminationNormal
	}
	return termination
}

func (s *StreamStatus) Summary() string {
	if s == nil {
		return "StreamStatus<nil>"
	}
	b := &strings.Builder{}
	fmt.Fprintf(b, "reason=%s", s.EndReason)
	if s.EndError != nil {
		fmt.Fprintf(b, " end_error=%q", s.EndError.Error())
	}
	s.mu.Lock()
	if s.ErrorCount > 0 {
		fmt.Fprintf(b, " soft_errors=%d", s.ErrorCount)
	}
	s.mu.Unlock()
	return b.String()
}
