package model

import (
	"bytes"
	"errors"
	"io"
	"math/big"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type selectorRandomFailure interface {
	error
	SelectorRandomFailure() bool
}

type boundedPatternReader struct {
	value    byte
	maxReads int
	reads    int
	err      error
	progress bool
}

func (reader *boundedPatternReader) Read(buffer []byte) (int, error) {
	reader.reads++
	if reader.reads > reader.maxReads {
		return 0, reader.err
	}
	if !reader.progress {
		return 0, nil
	}
	for index := range buffer {
		buffer[index] = reader.value
	}
	return len(buffer), nil
}

type singleByteReader struct {
	source *bytes.Reader
}

func (reader *singleByteReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 1 {
		buffer = buffer[:1]
	}
	return reader.source.Read(buffer)
}

type panicSelectorReader struct{}

func (panicSelectorReader) Read([]byte) (int, error) {
	panic("selector entropy panic")
}

func requireTypedSelectorRandomFailure(t *testing.T, err error) {
	t.Helper()
	var randomFailure selectorRandomFailure
	require.ErrorAs(t, err, &randomFailure)
	assert.True(t, randomFailure.SelectorRandomFailure())
}

func TestRandomBigIntBelowBoundsHostileReaders(t *testing.T) {
	sentinel := errors.New("reader bound reached")
	testCases := []struct {
		name       string
		reader     *boundedPatternReader
		errorText  string
		maxReadCap int
	}{
		{
			name: "persistent out of range",
			reader: &boundedPatternReader{
				value:    0xff,
				maxReads: 10_000,
				err:      sentinel,
				progress: true,
			},
			errorText:  "rejection",
			maxReadCap: 1_000,
		},
		{
			name: "zero progress",
			reader: &boundedPatternReader{
				maxReads: 10_000,
				err:      sentinel,
			},
			errorText:  "progress",
			maxReadCap: 100,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			selected, err := randomBigIntBelow(testCase.reader, big.NewInt(3))

			assert.Nil(t, selected)
			requireTypedSelectorRandomFailure(t, err)
			assert.Contains(t, err.Error(), testCase.errorText)
			assert.Less(t, testCase.reader.reads, testCase.maxReadCap)
			assert.NotErrorIs(t, err, sentinel, "the selector must stop before the hostile reader's test-only bound")
		})
	}
}

func TestRandomBigIntBelowHandlesShortReadsAndRejectionWithoutBias(t *testing.T) {
	shortSelected, err := randomBigIntBelow(
		&singleByteReader{source: bytes.NewReader([]byte{0, 1})},
		big.NewInt(257),
	)
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(1), shortSelected)

	rejectedThenAccepted, err := randomBigIntBelow(bytes.NewReader([]byte{3, 2}), big.NewInt(3))
	require.NoError(t, err)
	assert.Equal(t, big.NewInt(2), rejectedThenAccepted)

	for candidate := byte(0); candidate < 3; candidate++ {
		selected, selectErr := randomBigIntBelow(bytes.NewReader([]byte{candidate}), big.NewInt(3))
		require.NoError(t, selectErr)
		assert.Zero(t, selected.Cmp(big.NewInt(int64(candidate))))
	}
}

func TestRandomBigIntBelowTypedSourceFailuresAndPanic(t *testing.T) {
	sentinel := errors.New("rng failed")
	testCases := []struct {
		name      string
		reader    io.Reader
		errorIs   error
		errorText string
	}{
		{name: "eof", reader: bytes.NewReader(nil), errorIs: io.EOF, errorText: "read"},
		{name: "injected error", reader: &boundedPatternReader{err: sentinel}, errorIs: sentinel, errorText: "read"},
		{name: "reader panic", reader: panicSelectorReader{}, errorText: "panic"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			selected, err := randomBigIntBelow(testCase.reader, big.NewInt(3))

			assert.Nil(t, selected)
			requireTypedSelectorRandomFailure(t, err)
			assert.Contains(t, err.Error(), testCase.errorText)
			if testCase.errorIs != nil {
				assert.ErrorIs(t, err, testCase.errorIs)
			}
		})
	}
}

func TestRandomBigIntBelowLimitOneAndLargeLimit(t *testing.T) {
	selected, err := randomBigIntBelow(panicSelectorReader{}, big.NewInt(1))
	require.NoError(t, err)
	assert.Zero(t, selected.Sign())

	largeLimit := new(big.Int).Lsh(big.NewInt(1), 4096)
	selected, err = randomBigIntBelow(bytes.NewReader(make([]byte, 513)), largeLimit)
	require.NoError(t, err)
	assert.Zero(t, selected.Sign())
	assert.Less(t, selected.Cmp(largeLimit), 0)
}

func TestWeightedSelectorReturnsEligibleFallbackWithTypedEntropyFailure(t *testing.T) {
	priority := int64(100)
	maxWeight := ^uint(0)
	channels := make([]*Channel, 0, 1000)
	for id := 1; id <= 1000; id++ {
		weight := maxWeight
		channels = append(channels, &Channel{
			Id:       id,
			Priority: &priority,
			Weight:   &weight,
		})
	}
	reader := &boundedPatternReader{
		value:    0xff,
		maxReads: 10_000,
		err:      errors.New("reader bound reached"),
		progress: true,
	}

	selected, err := selectHighestPriorityWeightedChannelWithReader(channels, reader)

	require.Same(t, channels[0], selected)
	requireTypedSelectorRandomFailure(t, err)
	assert.Less(t, reader.reads, 1_000)
}

func TestWeightedSelectorConcurrentProductionDrawsRemainEligible(t *testing.T) {
	priority := int64(100)
	channels := make([]*Channel, 0, 1000)
	eligible := make(map[int]struct{}, 1000)
	for id := 1; id <= 1000; id++ {
		weight := uint(id)
		channels = append(channels, &Channel{Id: id, Priority: &priority, Weight: &weight})
		eligible[id] = struct{}{}
	}

	const workers = 64
	var waitGroup sync.WaitGroup
	errorsByWorker := make(chan error, workers)
	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			selected, err := selectHighestPriorityWeightedChannel(channels)
			if err != nil {
				errorsByWorker <- err
				return
			}
			if selected == nil {
				errorsByWorker <- errors.New("selector returned nil")
				return
			}
			if _, ok := eligible[selected.Id]; !ok {
				errorsByWorker <- errors.New("selector returned ineligible channel")
			}
		}()
	}
	waitGroup.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		require.NoError(t, err)
	}
}
