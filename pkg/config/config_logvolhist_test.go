package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidate_LogvolhistEnabled_RequiresQueryRangeStep(t *testing.T) {
	c := &Config{
		ServerGroups: []ServerGroup{{Name: "sg1", URL: "http://localhost:3100"}},
		API: APIConfig{
			Logvolhist: LogvolhistConfig{
				Enabled: true,
			},
		},
	}
	err := c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "query_range.step is required")
}

func TestValidate_LogvolhistEnabled_InvalidTimeout(t *testing.T) {
	c := &Config{
		ServerGroups: []ServerGroup{{Name: "sg1", URL: "http://localhost:3100"}},
		API: APIConfig{
			QueryRange: QueryRangeConfig{Step: "1m"},
			Logvolhist: LogvolhistConfig{
				Enabled: true,
				Timeout: "notaduration",
			},
		},
	}
	err := c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout")
}

func TestValidate_LogvolhistEnabled_MillisecondTimeout(t *testing.T) {
	c := &Config{
		ServerGroups: []ServerGroup{{Name: "sg1", URL: "http://localhost:3100"}},
		API: APIConfig{
			QueryRange: QueryRangeConfig{Step: "1m"},
			Logvolhist: LogvolhistConfig{
				Enabled: true,
				Timeout: "500ms",
			},
		},
	}
	err := c.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "milliseconds")
}

func TestValidate_LogvolhistEnabled_Valid(t *testing.T) {
	c := &Config{
		ServerGroups: []ServerGroup{{Name: "sg1", URL: "http://localhost:3100"}},
		API: APIConfig{
			QueryRange: QueryRangeConfig{Step: "1m"},
			Logvolhist: LogvolhistConfig{
				Enabled: true,
				Timeout: "30s",
			},
		},
	}
	err := c.Validate()
	require.NoError(t, err)
}

func TestValidate_LogvolhistDisabled_SkipsValidation(t *testing.T) {
	c := &Config{
		ServerGroups: []ServerGroup{{Name: "sg1", URL: "http://localhost:3100"}},
		API: APIConfig{
			Logvolhist: LogvolhistConfig{
				Enabled: false,
			},
		},
	}
	err := c.Validate()
	require.NoError(t, err)
}

func TestValidate_LogvolhistEnabled_NoTimeout_Valid(t *testing.T) {
	c := &Config{
		ServerGroups: []ServerGroup{{Name: "sg1", URL: "http://localhost:3100"}},
		API: APIConfig{
			QueryRange: QueryRangeConfig{Step: "1m"},
			Logvolhist: LogvolhistConfig{
				Enabled: true,
				Timeout: "", // optional
			},
		},
	}
	err := c.Validate()
	require.NoError(t, err)
}
