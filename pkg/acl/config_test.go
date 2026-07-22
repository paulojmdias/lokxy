package acl

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestACLConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ACLConfig
		wantErr string
	}{
		{
			name: "disabled skips validation",
			cfg: ACLConfig{
				Enabled: false,
				Rules:   []Rule{{Name: "", Action: "bogus"}},
			},
		},
		{
			name: "valid block and require rules",
			cfg: ACLConfig{
				Enabled: true,
				Rules: []Rule{
					{Name: "block-empty", Action: ActionBlock, Enforcement: EnforcementEnforce, When: []MatchCondition{{EmptySelector: true}}},
					{Name: "require-ns", Action: ActionRequireMatcher, Require: []RequireSpec{{Name: "namespace", Types: []string{"=", "=~"}}}},
				},
			},
		},
		{
			name:    "invalid default action",
			cfg:     ACLConfig{Enabled: true, DefaultAction: "warn"},
			wantErr: "default_action must be allow or block",
		},
		{
			name:    "missing rule name",
			cfg:     ACLConfig{Enabled: true, Rules: []Rule{{Action: ActionBlock}}},
			wantErr: "name is required",
		},
		{
			name: "duplicate rule name",
			cfg: ACLConfig{Enabled: true, Rules: []Rule{
				{Name: "dup", Action: ActionBlock},
				{Name: "dup", Action: ActionBlock},
			}},
			wantErr: "duplicate rule name",
		},
		{
			name:    "unknown action",
			cfg:     ACLConfig{Enabled: true, Rules: []Rule{{Name: "r", Action: "nope"}}},
			wantErr: "unknown action",
		},
		{
			name:    "missing action",
			cfg:     ACLConfig{Enabled: true, Rules: []Rule{{Name: "r"}}},
			wantErr: "action is required",
		},
		{
			name:    "invalid enforcement",
			cfg:     ACLConfig{Enabled: true, Rules: []Rule{{Name: "r", Action: ActionBlock, Enforcement: "loud"}}},
			wantErr: "enforcement must be enforce or warn",
		},
		{
			name: "invalid condition source",
			cfg: ACLConfig{Enabled: true, Rules: []Rule{
				{Name: "r", Action: ActionBlock, When: []MatchCondition{{Name: "x", Source: "cookie"}}},
			}},
			wantErr: "source must be query or header",
		},
		{
			name: "invalid condition type",
			cfg: ACLConfig{Enabled: true, Rules: []Rule{
				{Name: "r", Action: ActionBlock, When: []MatchCondition{{Name: "x", Types: []string{"~="}}}},
			}},
			wantErr: "not a valid matcher operator",
		},
		{
			name:    "require_matcher without require entries",
			cfg:     ACLConfig{Enabled: true, Rules: []Rule{{Name: "r", Action: ActionRequireMatcher}}},
			wantErr: "needs at least one require entry",
		},
		{
			name: "require entry missing name",
			cfg: ACLConfig{Enabled: true, Rules: []Rule{
				{Name: "r", Action: ActionRequireMatcher, Require: []RequireSpec{{Types: []string{"="}}}},
			}},
			wantErr: "require[0]: name is required",
		},
		{
			name: "require entry invalid type",
			cfg: ACLConfig{Enabled: true, Rules: []Rule{
				{Name: "r", Action: ActionRequireMatcher, Require: []RequireSpec{{Name: "ns", Types: []string{"=="}}}},
			}},
			wantErr: "require[0]: type \"==\" is not a valid matcher operator",
		},
		{
			name:    "inject_matcher without inject block",
			cfg:     ACLConfig{Enabled: true, Rules: []Rule{{Name: "r", Action: ActionInjectMatcher}}},
			wantErr: "needs an inject block with a name",
		},
		{
			name: "inject_matcher invalid type",
			cfg: ACLConfig{Enabled: true, Rules: []Rule{
				{Name: "r", Action: ActionInjectMatcher, Inject: &InjectSpec{Name: "cluster", Type: "=="}},
			}},
			wantErr: "is not a valid matcher operator",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
