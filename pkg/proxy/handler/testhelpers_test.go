package handler

import (
	"net/http"
	"testing"

	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/stretchr/testify/require"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

// Helper to wrap http.Response in BackendResponse for tests
func wrapResponse(resp *http.Response) *proxyresponse.BackendResponse {
	return &proxyresponse.BackendResponse{
		Response:    resp,
		BackendName: "test-backend",
		BackendURL:  "http://test-backend:3100",
	}
}

func TestStreamResultType(t *testing.T) {
	var sr StreamResult
	// ResultTypeStream is loghttp.ResultType("streams"); verify the method returns it.
	require.EqualValues(t, loghttp.ResultTypeStream, sr.Type())
}
