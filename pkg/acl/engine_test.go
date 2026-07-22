package acl

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewEngine_DisabledReturnsNil(t *testing.T) {
	require.Nil(t, NewEngine(ACLConfig{Enabled: false}))
	require.NotNil(t, NewEngine(ACLConfig{Enabled: true}))
}

func TestEngine_Evaluate_BlockEmptySelector(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:        "block-empty",
			Action:      ActionBlock,
			Enforcement: EnforcementEnforce,
			Reason:      "Queries must include at least one stream selector",
			When:        []MatchCondition{{EmptySelector: true}},
		}},
	})

	d := eng.Evaluate(mustSelectors(t, `{app=~".*"}`), nil)
	require.True(t, d.Reject)
	require.Equal(t, "block-empty", d.Rule)
	require.Contains(t, d.Reason, "stream selector")

	d = eng.Evaluate(mustSelectors(t, `{app="x"}`), nil)
	require.False(t, d.Reject)
}

func TestEngine_Evaluate_RequireMatcherAccumulates(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:   "require-ns-and-cluster",
			Action: ActionRequireMatcher,
			Reason: "payments needs scope",
			When:   []MatchCondition{{Name: "service", Value: "payments", Types: []string{"=", "=~"}}},
			Require: []RequireSpec{
				{Name: "namespace"},
				{Name: "cluster"},
			},
		}},
	})

	d := eng.Evaluate(mustSelectors(t, `{service="payments"}`), nil)
	require.True(t, d.Reject)
	// Both missing labels reported in a single response.
	require.Contains(t, d.Reason, "namespace")
	require.Contains(t, d.Reason, "cluster")

	// Satisfied query passes.
	d = eng.Evaluate(mustSelectors(t, `{service="payments", namespace="prod", cluster="eu"}`), nil)
	require.False(t, d.Reject)
}

func TestEngine_Evaluate_RequireMatcher_RegexConditionValue(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:    "require-ns",
			Action:  ActionRequireMatcher,
			When:    []MatchCondition{{Name: "service", Value: "payments", Types: []string{"=~"}}},
			Require: []RequireSpec{{Name: "namespace"}},
		}},
	})

	// service=~"pay.*" matches the condition value "payments".
	d := eng.Evaluate(mustSelectors(t, `{service=~"pay.*"}`), nil)
	require.True(t, d.Reject)

	// service=~"other.*" does not match "payments"; rule doesn't fire.
	d = eng.Evaluate(mustSelectors(t, `{service=~"other.*"}`), nil)
	require.False(t, d.Reject)
}

func TestEngine_Evaluate_WarnDoesNotReject(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:        "warn-empty",
			Action:      ActionBlock,
			Enforcement: EnforcementWarn,
			Reason:      "too broad",
			When:        []MatchCondition{{EmptySelector: true}},
		}},
	})

	d := eng.Evaluate(mustSelectors(t, `{}`), nil)
	require.False(t, d.Reject)
	require.Len(t, d.Warnings, 1)
	require.Equal(t, "warn-empty", d.Warnings[0].Rule)
	require.Len(t, d.Events, 1)
	require.Equal(t, outcomeWarned, d.Events[0].Outcome)
}

func TestEngine_Evaluate_HeaderCondition(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:   "block-bot",
			Action: ActionBlock,
			Reason: "no bots",
			When:   []MatchCondition{{Name: "X-Forwarded-User", Value: "bot", Source: SourceHeader}},
		}},
	})

	h := http.Header{}
	h.Set("X-Forwarded-User", "bot")
	d := eng.Evaluate(mustSelectors(t, `{app="x"}`), h)
	require.True(t, d.Reject)

	d = eng.Evaluate(mustSelectors(t, `{app="x"}`), http.Header{})
	require.False(t, d.Reject)
}

func TestEngine_Evaluate_HeaderAbsentAndPresence(t *testing.T) {
	// absent: fires when the header is not set.
	absent := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:   "require-user-header",
			Action: ActionBlock,
			Reason: "missing user",
			When:   []MatchCondition{{Name: "X-Forwarded-User", Source: SourceHeader, Absent: true}},
		}},
	})
	require.True(t, absent.Evaluate(mustSelectors(t, `{app="x"}`), http.Header{}).Reject)
	h := http.Header{}
	h.Set("X-Forwarded-User", "alice")
	require.False(t, absent.Evaluate(mustSelectors(t, `{app="x"}`), h).Reject)

	// presence: empty value fires when the header is set to anything.
	presence := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:   "block-any-user",
			Action: ActionBlock,
			When:   []MatchCondition{{Name: "X-Debug", Source: SourceHeader}},
		}},
	})
	hd := http.Header{}
	hd.Set("X-Debug", "1")
	require.True(t, presence.Evaluate(mustSelectors(t, `{app="x"}`), hd).Reject)
	require.False(t, presence.Evaluate(mustSelectors(t, `{app="x"}`), http.Header{}).Reject)
}

func TestEngine_Evaluate_QueryAbsentCondition(t *testing.T) {
	// absent: fires when the label is not present in the selector.
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{{
			Name:   "block-missing-env",
			Action: ActionBlock,
			Reason: "env label required",
			When:   []MatchCondition{{Name: "env", Absent: true}},
		}},
	})
	require.True(t, eng.Evaluate(mustSelectors(t, `{app="x"}`), nil).Reject)
	require.False(t, eng.Evaluate(mustSelectors(t, `{app="x", env="prod"}`), nil).Reject)
}

func TestEngine_Evaluate_AllowAndInjectAreNoOpsInPhase1(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules: []Rule{
			{Name: "allow-all", Action: ActionAllow},
			{Name: "inject-cluster", Action: ActionInjectMatcher, Inject: &InjectSpec{Name: "cluster", Value: "eu", Type: "="}},
			{Name: "block-empty", Action: ActionBlock, When: []MatchCondition{{EmptySelector: true}}},
		},
	})

	// allow does NOT short-circuit in Phase 1, so the block still applies.
	d := eng.Evaluate(mustSelectors(t, `{}`), nil)
	require.True(t, d.Reject)
	require.Equal(t, "block-empty", d.Rule)
}

func TestEngine_Evaluate_DefaultActionBlock(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled:       true,
		DefaultAction: ActionBlock,
		DefaultReason: "denied by default",
		Rules:         nil,
	})

	d := eng.Evaluate(mustSelectors(t, `{app="x"}`), nil)
	require.True(t, d.Reject)
	require.Equal(t, "denied by default", d.Reason)
}

func TestEngine_Evaluate_MultiSelectorAllMustPass(t *testing.T) {
	eng := NewEngine(ACLConfig{
		Enabled: true,
		Rules:   []Rule{{Name: "block-empty", Action: ActionBlock, When: []MatchCondition{{EmptySelector: true}}}},
	})

	// Binary op with one broad group must be rejected.
	sels := mustSelectors(t, `rate({app="a"}[5m]) / rate({app=~".*"}[5m])`)
	require.Len(t, sels, 2)
	d := eng.Evaluate(sels, nil)
	require.True(t, d.Reject)
}

func TestEngine_Evaluate_NilEngineForwards(t *testing.T) {
	var eng *Engine
	d := eng.Evaluate(mustSelectors(t, `{}`), nil)
	require.False(t, d.Reject)
}
