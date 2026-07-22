package acl

import (
	"fmt"
	"net/http"
	"strings"
)

// defaultRejectReason is used when a block or default-block rule carries no
// explicit reason.
const defaultRejectReason = "Query rejected by policy"

// Engine evaluates ACL rules against parsed queries. A nil Engine is a valid
// no-op: every query is forwarded. Engine is immutable after construction and
// safe for concurrent use.
type Engine struct {
	cfg ACLConfig
}

// NewEngine builds an Engine from configuration. It returns nil when ACL is
// disabled so that callers can treat "no engine" and "disabled" identically.
func NewEngine(cfg ACLConfig) *Engine {
	if !cfg.Enabled {
		return nil
	}
	return &Engine{cfg: cfg}
}

// Event records a single policy decision for observability. One event is
// emitted per rule that fires and per default-action fallback.
type Event struct {
	Rule        string
	Action      Action
	Enforcement Enforcement
	// Outcome is "blocked" or "warned".
	Outcome string
}

const (
	outcomeBlocked = "blocked"
	outcomeWarned  = "warned"
)

// Warning is a warn-mode match to surface on the response.
type Warning struct {
	Rule    string
	Message string
}

// Decision is the outcome of evaluating a request's queries. When Reject is
// true the caller must reject the query with Reason; otherwise the query is
// forwarded and any Warnings are surfaced as headers and logs.
type Decision struct {
	Reject   bool
	Reason   string
	Rule     string
	Warnings []Warning
	Events   []Event
}

// Evaluate runs every selector produced by a request through the rule set. The
// request is rejected as soon as one selector triggers an enforced block or an
// unsatisfied requirement; warnings from non-rejecting selectors accumulate.
func (e *Engine) Evaluate(selectors []selector, header http.Header) Decision {
	if e == nil {
		return Decision{}
	}

	var events []Event
	var warnings []Warning
	seenWarn := make(map[string]bool)

	for _, sel := range selectors {
		d := e.evaluateSelector(sel, header)
		events = append(events, d.Events...)
		if d.Reject {
			d.Events = events
			return d
		}
		for _, w := range d.Warnings {
			if !seenWarn[w.Rule] {
				seenWarn[w.Rule] = true
				warnings = append(warnings, w)
			}
		}
	}

	return Decision{Warnings: warnings, Events: events}
}

// evaluateSelector runs the single-pass rule loop for one selector.
func (e *Engine) evaluateSelector(sel selector, header http.Header) Decision {
	var events []Event
	var warnings []Warning
	var violations []string
	anyMatched := false

	for _, rule := range e.cfg.Rules {
		if !ruleMatches(rule, sel, header) {
			continue
		}
		anyMatched = true

		switch rule.Action {
		case ActionAllow, ActionInjectMatcher:
			// Reserved for Phase 2. Validated but not evaluated here.
			continue

		case ActionBlock:
			if enforcementOf(rule) == EnforcementWarn {
				events = append(events, event(rule, outcomeWarned))
				warnings = append(warnings, Warning{Rule: rule.Name, Message: reasonOf(rule)})
				continue
			}
			events = append(events, event(rule, outcomeBlocked))
			return Decision{
				Reject:   true,
				Reason:   reasonOf(rule),
				Rule:     rule.Name,
				Warnings: warnings,
				Events:   events,
			}

		case ActionRequireMatcher:
			missing := missingRequires(rule, sel)
			if len(missing) == 0 {
				continue
			}
			msg := requireReason(rule, missing)
			if enforcementOf(rule) == EnforcementWarn {
				events = append(events, event(rule, outcomeWarned))
				warnings = append(warnings, Warning{Rule: rule.Name, Message: msg})
				continue
			}
			events = append(events, event(rule, outcomeBlocked))
			violations = append(violations, msg)
		}
	}

	if len(violations) > 0 {
		return Decision{
			Reject:   true,
			Reason:   strings.Join(violations, "; "),
			Rule:     "require_matcher",
			Warnings: warnings,
			Events:   events,
		}
	}

	if !anyMatched && e.cfg.DefaultAction == ActionBlock {
		reason := e.cfg.DefaultReason
		if reason == "" {
			reason = "Query rejected by default policy"
		}
		events = append(events, Event{Rule: "default", Action: ActionBlock, Enforcement: EnforcementEnforce, Outcome: outcomeBlocked})
		return Decision{Reject: true, Reason: reason, Rule: "default", Events: events}
	}

	return Decision{Warnings: warnings, Events: events}
}

// ruleMatches reports whether all of a rule's conditions match (AND). A rule
// with no conditions matches every query.
func ruleMatches(r Rule, sel selector, header http.Header) bool {
	for _, cond := range r.When {
		if !conditionMatches(cond, sel, header) {
			return false
		}
	}
	return true
}

// missingRequires returns the names of required matchers absent from the
// selector.
func missingRequires(r Rule, sel selector) []string {
	var missing []string
	for _, req := range r.Require {
		if !sel.satisfiesRequire(req) {
			missing = append(missing, req.Name)
		}
	}
	return missing
}

func requireReason(r Rule, missing []string) string {
	reason := reasonOf(r)
	return fmt.Sprintf("%s (missing required label(s): %s)", reason, strings.Join(missing, ", "))
}

func reasonOf(r Rule) string {
	if strings.TrimSpace(r.Reason) != "" {
		return strings.TrimSpace(r.Reason)
	}
	return defaultRejectReason
}

func enforcementOf(r Rule) Enforcement {
	if r.Enforcement == "" {
		return EnforcementEnforce
	}
	return r.Enforcement
}

func event(r Rule, outcome string) Event {
	return Event{Rule: r.Name, Action: r.Action, Enforcement: enforcementOf(r), Outcome: outcome}
}
