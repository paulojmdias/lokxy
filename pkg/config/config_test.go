package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name         string
		configFile   string
		wantErr      bool
		validateFunc func(*testing.T, *Config)
	}{
		{
			name:       "valid basic config",
			configFile: "testdata/valid_config.yaml",
			wantErr:    false,
			validateFunc: func(t *testing.T, cfg *Config) {
				require.Len(t, cfg.ServerGroups, 2)

				// Verify first server group
				require.Equal(t, "loki1", cfg.ServerGroups[0].Name)
				require.Equal(t, "http://loki1.example.com", cfg.ServerGroups[0].URL)
				require.Equal(t, 10, cfg.ServerGroups[0].Timeout)
				require.Equal(t, "Bearer token1", cfg.ServerGroups[0].Headers["Authorization"])

				// Verify second server group
				require.Equal(t, "loki2", cfg.ServerGroups[1].Name)
				require.Equal(t, "http://loki2.example.com", cfg.ServerGroups[1].URL)
				require.Equal(t, 15, cfg.ServerGroups[1].Timeout)

				// Verify logging config
				require.Equal(t, "info", cfg.Logging.Level)
				require.Equal(t, "json", cfg.Logging.Format)
			},
		},
		{
			name:       "minimal config",
			configFile: "testdata/minimal_config.yaml",
			wantErr:    false,
			validateFunc: func(t *testing.T, cfg *Config) {
				require.Len(t, cfg.ServerGroups, 1)

				require.Equal(t, "loki1", cfg.ServerGroups[0].Name)
				require.Equal(t, "http://localhost:3100", cfg.ServerGroups[0].URL)
				require.Equal(t, 0, cfg.ServerGroups[0].Timeout) // Default not set in config
			},
		},
		{
			name:       "full config with TLS",
			configFile: "testdata/full_config.yaml",
			wantErr:    false,
			validateFunc: func(t *testing.T, cfg *Config) {
				require.Len(t, cfg.ServerGroups, 2)

				// Verify first server group with full TLS config
				require.Equal(t, "loki-production", cfg.ServerGroups[0].Name)
				require.Equal(t, "https://loki.example.com", cfg.ServerGroups[0].URL)
				require.Equal(t, 30, cfg.ServerGroups[0].Timeout)
				require.Equal(t, "Bearer prod-token", cfg.ServerGroups[0].Headers["Authorization"])
				require.Equal(t, "tenant-1", cfg.ServerGroups[0].Headers["X-Scope-OrgID"])

				// Verify TLS config
				require.False(t, cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.InsecureSkipVerify)
				require.Equal(t, "/etc/ssl/certs/ca.crt", cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.CAFile)
				require.Equal(t, "/etc/ssl/certs/client.crt", cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.CertFile)
				require.Equal(t, "/etc/ssl/private/client.key", cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.KeyFile)

				// Verify transport config (explicit values)
				tc := cfg.ServerGroups[0].HTTPClientConfig.Transport
				require.False(t, tc.DisableKeepAlives)
				require.Equal(t, 200, tc.MaxIdleConns)
				require.Equal(t, 50, tc.MaxIdleConnsPerHost)
				require.Equal(t, 120*time.Second, tc.IdleConnTimeout)
				require.Equal(t, 2*time.Second, tc.ExpectContinueTimeout)
				require.Equal(t, 25*time.Second, tc.ResponseHeaderTimeout)
				require.NotNil(t, tc.ForceAttemptHTTP2)
				require.True(t, *tc.ForceAttemptHTTP2)

				// Verify staging has zero-value transport (no transport block)
				tc2 := cfg.ServerGroups[1].HTTPClientConfig.Transport
				require.False(t, tc2.DisableKeepAlives)
				require.Equal(t, 0, tc2.MaxIdleConns)
				require.Equal(t, 0, tc2.MaxIdleConnsPerHost)
				require.Equal(t, time.Duration(0), tc2.IdleConnTimeout)
				require.Equal(t, time.Duration(0), tc2.ExpectContinueTimeout)
				require.Equal(t, time.Duration(0), tc2.ResponseHeaderTimeout)
				require.Nil(t, tc2.ForceAttemptHTTP2)

				// Verify logging config
				require.Equal(t, "debug", cfg.Logging.Level)
				require.Equal(t, "logfmt", cfg.Logging.Format)
			},
		},
		{
			name:       "TLS config only",
			configFile: "testdata/tls_config.yaml",
			wantErr:    false,
			validateFunc: func(t *testing.T, cfg *Config) {
				require.Len(t, cfg.ServerGroups, 1)

				require.Equal(t, "secure-loki", cfg.ServerGroups[0].Name)
				require.Equal(t, "https://loki.secure.example.com", cfg.ServerGroups[0].URL)
				require.Equal(t, "/path/to/ca.crt", cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.CAFile)

				require.Equal(t, "warn", cfg.Logging.Level)
			},
		},
		{
			name:       "error handling fields",
			configFile: "testdata/error_handling_config.yaml",
			wantErr:    false,
			validateFunc: func(t *testing.T, cfg *Config) {
				require.Len(t, cfg.ServerGroups, 3)

				// Defaults: both flags false when unset.
				require.False(t, cfg.ServerGroups[0].IgnoreError)
				require.False(t, cfg.ServerGroups[0].DowngradeError)

				require.True(t, cfg.ServerGroups[1].IgnoreError)
				require.False(t, cfg.ServerGroups[1].DowngradeError)

				require.False(t, cfg.ServerGroups[2].IgnoreError)
				require.True(t, cfg.ServerGroups[2].DowngradeError)
			},
		},
		{
			name:       "invalid empty config",
			configFile: "testdata/invalid_empty.yaml",
			wantErr:    true,
		},
		{
			name:       "invalid both error handling flags",
			configFile: "testdata/invalid_both_error_handling.yaml",
			wantErr:    true,
		},
		{
			name:       "invalid syntax",
			configFile: "testdata/invalid_syntax.yaml",
			wantErr:    true,
		},
		{
			name:       "non-existent file",
			configFile: "testdata/does_not_exist.yaml",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadConfig(tt.configFile)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)

			if tt.validateFunc != nil {
				tt.validateFunc(t, cfg)
			}
		})
	}
}

func TestSetReadyAndIsReady(t *testing.T) {
	// Ensure initial state is cleaned up after the test
	t.Cleanup(func() { SetReady(false) })

	SetReady(true)
	require.True(t, IsReady())

	SetReady(false)
	require.False(t, IsReady())
}

func TestValidate_EmptyServerGroups(t *testing.T) {
	cfg := &Config{}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one server group")
}

func TestValidate_MissingName(t *testing.T) {
	cfg := &Config{
		ServerGroups: []ServerGroup{{URL: "http://localhost:3100"}},
	}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "name is required")
}

func TestValidate_MissingURL(t *testing.T) {
	cfg := &Config{
		ServerGroups: []ServerGroup{{Name: "loki1"}},
	}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "url is required")
}

func TestValidate_MutuallyExclusiveErrorHandling(t *testing.T) {
	cfg := &Config{
		ServerGroups: []ServerGroup{{
			Name:           "loki1",
			URL:            "http://localhost:3100",
			IgnoreError:    true,
			DowngradeError: true,
		}},
	}
	err := cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "mutually exclusive")
}
