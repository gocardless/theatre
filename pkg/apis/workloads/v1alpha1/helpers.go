package v1alpha1

import (
	"time"

	"github.com/pkg/errors"
)

// Pending returns true if the console is Pending
func (c *Console) Pending() bool {
	return c.Status.Phase == ConsolePending
}

// Running returns true if the console is Running
func (c *Console) Running() bool {
	return c.Status.Phase == ConsoleRunning
}

// Stopped returns true if the console is Stopped
func (c *Console) Stopped() bool {
	return c.Status.Phase == ConsoleStopped
}

// EligibleForGC returns true if the console can be garbage collected. This is the case if
// its TTLSecondsAfterFinished has elapsed.
func (c *Console) EligibleForGC() bool {
	if !c.Stopped() {
		return false
	}

	if c.Status.ExpiryTime == nil {
		return false
	}

	// When the console is completed
	if c.Status.CompletionTime != nil {
		return c.Status.CompletionTime.Time.Add(c.TTLDuration()).Before(time.Now())
	}

	return c.Status.ExpiryTime.Time.Add(c.TTLDuration()).Before(time.Now())
}

// TTLDuration returns the console's TTL as a time.Duration
func (c *Console) TTLDuration() time.Duration {
	return time.Duration(*c.Spec.TTLSecondsAfterFinished) * time.Second
}

// GetDefaultCommandWithArgs returns a concatenated list of command and
// arguments, if defined on the template
func (ct *ConsoleTemplate) GetDefaultCommandWithArgs() ([]string, error) {
	containers := ct.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		return []string{}, errors.New("template has no containers defined")
	}

	return append(containers[0].Command, containers[0].Args...), nil
}

// GetAuthorisationRuleForCommand returns an authorisation rule that matches
// the command that a console is being started with, or an error if one does
// not exist.
//
// It does this by iterating through the console template's authorisation rules
// list until it finds a match, and then falls back to the default
// authorisation rule if one is defined.
//
// The `matchCommandElements` field, within an AuthorisationRule, is an array
// of matchers, of which there are 3 supported types:
//
//   1. `*`  - a wildcard that matches the presence of an element.
//   2. `**` - a wildcard that matches any number (including 0) of
//             elements. This can only be used at the end of the array.
//   3. Any other string of characters, this is used to perform an exact
//      string match against the current element.
//
// The elements of the command array are evaluated in order; any failure to
// match will result in falling back to the next rule.
//
// Examples:
//
// | Matcher               | Command                          | Matches? |
// | --------------------- | -------------------------------- | -------- |
// | ["bash"]              | ["bash"]                         | Yes      |
// | ["ls", "*"]           | ["ls"]                           | No       |
// | ["ls", "*"]           | ["ls", "file"]                   | Yes      |
// | ["ls", "*", "file2"]  | ["ls", "file", "file3", "file2"] | No       |
// | ["ls", "*", "file2"]  | ["ls", "file", "file2"]          | Yes      |
// | ["echo", "**"]        | ["echo"]                         | Yes      |
// | ["echo", "**"]        | ["echo", "hello"]                | Yes      |
// | ["echo", "**"]        | ["echo", "hi", "bye" ]           | Yes      |
// | ["echo", "**", "bye"] | ["echo", "hi", "bye" ]           | Error    |
//
func (ct *ConsoleTemplate) GetAuthorisationRuleForCommand(command []string) (ConsoleAuthorisationRule, error) {
	for _, rule := range ct.Spec.AuthorisationRules {
	matchElement:
		for i, element := range rule.MatchCommandElements {
			switch element {
			case "*":
				// Check only that there is an element at the position being evaluated.
				if len(command) < (i + 1) {
					break matchElement
				}

			case "**":
				// By only supporting double wildcards at the end we keep the logic
				// simple, as there's no need to backtrack.
				if (i + 1) < len(rule.MatchCommandElements) {
					return rule, errors.New("a double wildcard is only valid at the end of the pattern")
				}

				// 'Exit early', because we want to match on anything (or nothing)
				// subsequent to this.
				return rule, nil

			case "":
				return rule, errors.New("an empty match element is not valid")

			default:
				// Treat everything else as an exact string match. Test that the
				// element exists first, because the match pattern may be longer than
				// what is being tested.
				if len(command) < (i+1) || command[i] != element {
					break matchElement
				}
			}

			// If we're at the end of this rule and we haven't already returned then
			// we've fully matched the command.
			if (i + 1) == len(rule.MatchCommandElements) {
				return rule, nil
			}
		}
	}

	if ct.Spec.DefaultAuthorisationRule != nil {
		rule := ConsoleAuthorisationRule{
			Name:               "default",
			ConsoleAuthorisers: *ct.Spec.DefaultAuthorisationRule,
		}

		return rule, nil
	}

	return ConsoleAuthorisationRule{}, errors.New("no rules matched the command")
}

// HasAuthorisationRules defines whether a console template has authorisation
// rules defined on it.
func (ct *ConsoleTemplate) HasAuthorisationRules() bool {
	if len(ct.Spec.AuthorisationRules) > 0 || ct.Spec.DefaultAuthorisationRule != nil {
		return true
	}

	return false
}
