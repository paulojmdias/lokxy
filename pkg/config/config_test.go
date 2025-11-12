package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
				require.NotNil(t, cfg)
				require.Len(t, cfg.ServerGroups, 2)

				// Verify first server group
				assert.Equal(t, "loki1", cfg.ServerGroups[0].Name)
				assert.Equal(t, "http://loki1.example.com", cfg.ServerGroups[0].URL)
				assert.Equal(t, 10, cfg.ServerGroups[0].Timeout)
				assert.Equal(t, "Bearer token1", cfg.ServerGroups[0].Headers["Authorization"])

				// Verify second server group
				assert.Equal(t, "loki2", cfg.ServerGroups[1].Name)
				assert.Equal(t, "http://loki2.example.com", cfg.ServerGroups[1].URL)
				assert.Equal(t, 15, cfg.ServerGroups[1].Timeout)

				// Verify logging config
				assert.Equal(t, "info", cfg.Logging.Level)
				assert.Equal(t, "json", cfg.Logging.Format)
			},
		},
		{
			name:       "minimal config",
			configFile: "testdata/minimal_config.yaml",
			wantErr:    false,
			validateFunc: func(t *testing.T, cfg *Config) {
				require.NotNil(t, cfg)
				require.Len(t, cfg.ServerGroups, 1)

				assert.Equal(t, "loki1", cfg.ServerGroups[0].Name)
				assert.Equal(t, "http://localhost:3100", cfg.ServerGroups[0].URL)
				assert.Equal(t, 0, cfg.ServerGroups[0].Timeout) // Default not set in config
			},
		},
		{
			name:       "full config with TLS",
			configFile: "testdata/full_config.yaml",
			wantErr:    false,
			validateFunc: func(t *testing.T, cfg *Config) {
				require.NotNil(t, cfg)
				require.Len(t, cfg.ServerGroups, 2)

				// Verify first server group with full TLS config
				assert.Equal(t, "loki-production", cfg.ServerGroups[0].Name)
				assert.Equal(t, "https://loki.example.com", cfg.ServerGroups[0].URL)
				assert.Equal(t, 30, cfg.ServerGroups[0].Timeout)
				assert.Equal(t, "Bearer prod-token", cfg.ServerGroups[0].Headers["Authorization"])
				assert.Equal(t, "tenant-1", cfg.ServerGroups[0].Headers["X-Scope-OrgID"])

				// Verify TLS config
				assert.False(t, cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.InsecureSkipVerify)
				assert.Equal(t, "/etc/ssl/certs/ca.crt", cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.CAFile)
				assert.Equal(t, "/etc/ssl/certs/client.crt", cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.CertFile)
				assert.Equal(t, "/etc/ssl/private/client.key", cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.KeyFile)

				// Verify logging config
				assert.Equal(t, "debug", cfg.Logging.Level)
				assert.Equal(t, "logfmt", cfg.Logging.Format)
			},
		},
		{
			name:       "TLS config only",
			configFile: "testdata/tls_config.yaml",
			wantErr:    false,
			validateFunc: func(t *testing.T, cfg *Config) {
				require.NotNil(t, cfg)
				require.Len(t, cfg.ServerGroups, 1)

				assert.Equal(t, "secure-loki", cfg.ServerGroups[0].Name)
				assert.Equal(t, "https://loki.secure.example.com", cfg.ServerGroups[0].URL)
				assert.Equal(t, "/path/to/ca.crt", cfg.ServerGroups[0].HTTPClientConfig.TLSConfig.CAFile)

				assert.Equal(t, "warn", cfg.Logging.Level)
			},
		},
		{
			name:       "invalid empty config",
			configFile: "testdata/invalid_empty.yaml",
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
