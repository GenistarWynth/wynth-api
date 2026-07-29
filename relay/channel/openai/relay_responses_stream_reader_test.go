package openai

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emptyThenEOFReader struct {
	returnedEmpty bool
}

func (r *emptyThenEOFReader) Read(_ []byte) (int, error) {
	if !r.returnedEmpty {
		r.returnedEmpty = true
		return 0, nil
	}
	return 0, io.EOF
}

type noProgressReader struct{}

func (noProgressReader) Read(_ []byte) (int, error) {
	return 0, nil
}

func TestRemoteCompactionStreamReaderAllowsExactLimit(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), compactResponseStreamReadLimit)
	reader := &remoteCompactionStreamReader{
		ReadCloser: io.NopCloser(bytes.NewReader(payload)),
		remaining:  compactResponseStreamReadLimit,
	}

	actual, err := io.ReadAll(reader)

	require.NoError(t, err)
	require.Equal(t, payload, actual)
}

func TestRemoteCompactionStreamReaderRejectsLimitPlusOne(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), compactResponseStreamReadLimit+1)
	source := &countingReadCloser{reader: bytes.NewReader(payload)}
	reader := &remoteCompactionStreamReader{
		ReadCloser: source,
		remaining:  compactResponseStreamReadLimit,
	}

	actual, err := io.ReadAll(reader)

	require.ErrorIs(t, err, errRemoteCompactionStreamTooLarge)
	require.Equal(t, payload[:compactResponseStreamReadLimit], actual)
	require.Equal(t, int64(compactResponseStreamReadLimit+1), source.bytesRead.Load())

	n, err := reader.Read(make([]byte, 1))
	assert.Zero(t, n)
	require.ErrorIs(t, err, errRemoteCompactionStreamTooLarge)
	assert.Equal(t, int64(compactResponseStreamReadLimit+1), source.bytesRead.Load())
}

func TestRemoteCompactionStreamReaderRetriesEmptyLimitProbe(t *testing.T) {
	reader := &remoteCompactionStreamReader{
		ReadCloser: io.NopCloser(&emptyThenEOFReader{}),
	}

	n, err := reader.Read(make([]byte, 1))

	assert.Zero(t, n)
	require.ErrorIs(t, err, io.EOF)
}

func TestRemoteCompactionStreamReaderStopsAfterNoProgressAtLimit(t *testing.T) {
	reader := &remoteCompactionStreamReader{
		ReadCloser: io.NopCloser(noProgressReader{}),
	}

	actual, err := io.ReadAll(reader)

	assert.Empty(t, actual)
	require.ErrorIs(t, err, io.ErrNoProgress)
}
