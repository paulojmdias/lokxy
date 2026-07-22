// Package acl implements a query-level access-control engine for lokxy.
//
// lokxy is a fan-out proxy: every incoming Loki query is forwarded to every
// configured backend. Without any guardrail a single broad query such as
// {app=~".*"} is multiplied across all backends. The ACL engine intercepts
// LogQL queries before fan-out, evaluates operator-defined structural rules
// against the stream selectors and request headers, and either blocks the
// query or forwards it with a warning.
//
// This package implements Phase 1 of RFC 0001: the block and require_matcher
// actions. The allow and inject_matcher actions are parsed and validated but
// are not yet evaluated; they are reserved for Phase 2.
package acl

import "fmt"

// Action is the effect a rule has when its conditions match.
type Action string

const (
	// ActionAllow short-circuits evaluation and forwards the query as-is.
	// Reserved for Phase 2; validated but not evaluated in Phase 1.
	ActionAllow Action = "allow"
	// ActionBlock rejects the query.
	ActionBlock Action = "block"
	// ActionRequireMatcher rejects the query unless it carries the required
	// label matchers.
	ActionRequireMatcher Action = "require_matcher"
	// ActionInjectMatcher rewrites the query to add default matchers.
	// Reserved for Phase 2; validated but not evaluated in Phase 1.
	ActionInjectMatcher Action = "inject_matcher"
)

// Enforcement controls whether a matching rule rejects the query or merely
// warns about it.
type Enforcement string

const (
	// EnforcementEnforce rejects the query with HTTP 400.
	EnforcementEnforce Enforcement = "enforce"
	// EnforcementWarn forwards the query and surfaces a warning header and log.
	EnforcementWarn Enforcement = "warn"
)

// Source selects what a MatchCondition is tested against.
type Source string

const (
	// SourceQuery tests against the LogQL stream selector labels (default).
	SourceQuery Source = "query"
	// SourceHeader tests against an HTTP request header.
	SourceHeader Source = "header"
)

// ACLConfig is the top-level `acl:` configuration block.
type ACLConfig struct {
	Enabled bool `yaml:"enabled"`

	// DefaultAction applies when no rule matches. allow (default) forwards the
	// query; block rejects it with DefaultReason.
	DefaultAction Action `yaml:"default_action"`
	DefaultReason string `yaml:"default_reason"`

	Rules []Rule `yaml:"rules"`
}

// Rule is a single ACL rule evaluated in declaration order.
type Rule struct {
	Name        string           `yaml:"name"`
	Action      Action           `yaml:"action"`
	Enforcement Enforcement      `yaml:"enforcement"`
	Reason      string           `yaml:"reason"`
	When        []MatchCondition `yaml:"when"`

	// Require lists the matchers a query must carry, for require_matcher rules.
	Require []RequireSpec `yaml:"require"`

	// Inject is the matcher an inject_matcher rule adds. Reserved for Phase 2.
	Inject *InjectSpec `yaml:"inject"`
}

// MatchCondition is one predicate in a rule's `when` block. All conditions in a
// rule must match (AND logic). A rule with no conditions matches every query.
type MatchCondition struct {
	Name  string   `yaml:"name"`
	Value string   `yaml:"value"`
	Types []string `yaml:"types"`
	// Source selects query (default) or header. Empty means query.
	Source Source `yaml:"source"`
	// Absent fires when the label is NOT present in the selector.
	Absent bool `yaml:"absent"`
	// EmptySelector fires when the query has no meaningful stream selector
	// (literally empty, or only catch-all matchers).
	EmptySelector bool `yaml:"empty_selector"`
}

// RequireSpec is a matcher a query must carry for a require_matcher rule.
type RequireSpec struct {
	Name  string   `yaml:"name"`
	Types []string `yaml:"types"`
}

// InjectSpec is the matcher an inject_matcher rule adds. Reserved for Phase 2.
type InjectSpec struct {
	Name     string `yaml:"name"`
	Value    string `yaml:"value"`
	Type     string `yaml:"type"`
	IfAbsent bool   `yaml:"if_absent"`
}

// matcherTypes is the set of valid LogQL matcher operators accepted in `types`.
var matcherTypes = map[string]bool{"=": true, "!=": true, "=~": true, "!~": true}

// Validate checks the ACL configuration for structural errors. It is a no-op
// when ACL is disabled.
func (c *ACLConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	switch c.DefaultAction {
	case "", ActionAllow, ActionBlock:
		// ok; empty defaults to allow at evaluation time.
	default:
		return fmt.Errorf("acl: default_action must be allow or block, got %q", c.DefaultAction)
	}

	seen := make(map[string]bool, len(c.Rules))
	for i, r := range c.Rules {
		if r.Name == "" {
			return fmt.Errorf("acl: rules[%d]: name is required", i)
		}
		if seen[r.Name] {
			return fmt.Errorf("acl: rules[%d]: duplicate rule name %q", i, r.Name)
		}
		seen[r.Name] = true

		if err := r.validate(i); err != nil {
			return err
		}
	}
	return nil
}

func (r *Rule) validate(i int) error {
	switch r.Action {
	case ActionAllow, ActionBlock, ActionRequireMatcher, ActionInjectMatcher:
	case "":
		return fmt.Errorf("acl: rules[%d] (%s): action is required", i, r.Name)
	default:
		return fmt.Errorf("acl: rules[%d] (%s): unknown action %q", i, r.Name, r.Action)
	}

	switch r.Enforcement {
	case "", EnforcementEnforce, EnforcementWarn:
		// ok; empty defaults to enforce at evaluation time.
	default:
		return fmt.Errorf("acl: rules[%d] (%s): enforcement must be enforce or warn, got %q", i, r.Name, r.Enforcement)
	}

	for j, cond := range r.When {
		switch cond.Source {
		case "", SourceQuery, SourceHeader:
		default:
			return fmt.Errorf("acl: rules[%d] (%s): when[%d]: source must be query or header, got %q", i, r.Name, j, cond.Source)
		}
		if err := validateTypes(cond.Types); err != nil {
			return fmt.Errorf("acl: rules[%d] (%s): when[%d]: %w", i, r.Name, j, err)
		}
	}

	if r.Action == ActionRequireMatcher {
		if len(r.Require) == 0 {
			return fmt.Errorf("acl: rules[%d] (%s): require_matcher needs at least one require entry", i, r.Name)
		}
		for j, req := range r.Require {
			if req.Name == "" {
				return fmt.Errorf("acl: rules[%d] (%s): require[%d]: name is required", i, r.Name, j)
			}
			if err := validateTypes(req.Types); err != nil {
				return fmt.Errorf("acl: rules[%d] (%s): require[%d]: %w", i, r.Name, j, err)
			}
		}
	}

	if r.Action == ActionInjectMatcher {
		if r.Inject == nil || r.Inject.Name == "" {
			return fmt.Errorf("acl: rules[%d] (%s): inject_matcher needs an inject block with a name", i, r.Name)
		}
		if r.Inject.Type != "" && !matcherTypes[r.Inject.Type] {
			return fmt.Errorf("acl: rules[%d] (%s): inject.type %q is not a valid matcher operator", i, r.Name, r.Inject.Type)
		}
	}

	return nil
}

func validateTypes(types []string) error {
	for _, t := range types {
		if !matcherTypes[t] {
			return fmt.Errorf("type %q is not a valid matcher operator (=, !=, =~, !~)", t)
		}
	}
	return nil
}
