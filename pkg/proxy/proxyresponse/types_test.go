package proxyresponse

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
