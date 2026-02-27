package proxyresponse

import (
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardBackendError(t *testing.T) {
	logger := log.NewNopLogger()
	w := httptest.NewRecorder()

	ForwardBackendError(w, "loki1", 503, []byte("upstream unavailable"), logger)

	resp := w.Result()
	require.Equal(t, 503, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, "loki1", resp.Header.Get("Failed-Backend"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "loki1: upstream unavailable", string(body))
}

func TestForwardConnectionError(t *testing.T) {
	logger := log.NewNopLogger()
	w := httptest.NewRecorder()

	backendErr := &BackendError{
		BackendName: "loki2",
		BackendURL:  "http://loki2:3100",
		Err:         errors.New("connection refused"),
	}
	ForwardConnectionError(w, backendErr, logger)

	resp := w.Result()
	require.Equal(t, 502, resp.StatusCode)
	require.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	require.Equal(t, "loki2", resp.Header.Get("Failed-Backend"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "loki2: connection refused", string(body))
}

func TestBackendError(t *testing.T) {
	t.Run("with error", func(t *testing.T) {
		err := &BackendError{
			Err: io.ErrUnexpectedEOF,
		}
		assert.Equal(t, io.ErrUnexpectedEOF.Error(), err.Error())
		assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})

	t.Run("without error", func(t *testing.T) {
		err := &BackendError{BackendName: "up1"}
		assert.Equal(t, "error in upstream up1", err.Error())
	})
}
