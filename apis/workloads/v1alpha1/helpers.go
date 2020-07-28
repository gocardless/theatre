package v1alpha1

import (
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

// Creating returns true if the console has no status (the console has just been created)
func (c *Console) Creating() bool {
	return c.Status.Phase == ""
}

// PendingAuthorisation returns true if the is Pending Authorisation
func (c *Console) PendingAuthorisation() bool {
	return c.Status.Phase == ConsolePendingAuthorisation
}

// PendingJob returns true if the console is in a phase that occurs before job
// creation
func (c *Console) PendingJob() bool {
	return c.Creating() || c.PendingAuthorisation()
}

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

// Destroyed returns true if the console is Destroyed
func (c *Console) Destroyed() bool {
	return c.Status.Phase == ConsoleDestroyed
}

// PreRunning returns true if the console is in a phase before Running
func (c *Console) PreRunning() bool {
	return c.Creating() || c.PendingAuthorisation() || c.Pending()
}

// PostRunning returns true if the console is in a phase after Running
func (c *Console) PostRunning() bool {
	return c.Stopped() || c.Destroyed()
}

// EligibleForGC returns whether a console can be garbage collected
func (c *Console) EligibleForGC() bool {
	gcTime := c.GetGCTime()
	if gcTime == nil {
		return false
	}

	return gcTime.Before(time.Now())
}

// GetGCTime returns time time at which a console can be garbage collected, or
// nil if it cannot be.
//
// This will be the case if:
// - TTLSecondsBeforeRunning has elapsed and the console hasn't progressed to running
// - TTLSecondsAfterFinished has elapsed and the console is stopped or destroyed
func (c *Console) GetGCTime() *time.Time {
	switch {
	case c.PreRunning():
		// When the console hasn't progressed to the running phase
		t := c.CreationTimestamp.Add(c.TTLSecondsBeforeRunning())
		return &t
	case c.PostRunning():
		// When the console is completed
		if c.Status.CompletionTime != nil {
			t := c.Status.CompletionTime.Time.Add(c.TTLSecondsAfterFinished())
			return &t
		}
		// When the console never completed
		t := c.Status.ExpiryTime.Time.Add(c.TTLSecondsAfterFinished())
		return &t
	}

	return nil
}

// TTLSecondsAfterFinished returns the console's after finished TTL as a time.Duration
func (c *Console) TTLSecondsAfterFinished() time.Duration {
	return time.Duration(*c.Spec.TTLSecondsAfterFinished) * time.Second
}

// TTLSecondsBeforeRunning returns the console's before running TTL as a time.Duration
func (c *Console) TTLSecondsBeforeRunning() time.Duration {
	return time.Duration(*c.Spec.TTLSecondsBeforeRunning) * time.Second
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
	// We expect that the Validate() function will already have been called
	// before this, via the webhook that validates console templates. However,
	// perform the check again here because the logic below depends upon the
	// validity of these rules, and this expectation may not hold true when the
	// function is called in other contexts, e.g. unit tests.
	if err := ct.Validate(); err != nil {
		return ConsoleAuthorisationRule{}, err
	}

matchRule:
	for _, rule := range ct.Spec.AuthorisationRules {
		numMatchers := len(rule.MatchCommandElements)

		// Assert that the command provided matches the number of elements defined
		// in the matchers array, but if the last matcher is '**' then the last
		// element of the command is optional as well as anything following it.
		if rule.MatchCommandElements[numMatchers-1] == "**" {
			if len(command) < numMatchers-1 {
				break matchRule
			}
		} else {
			if len(command) != numMatchers {
				break matchRule
			}
		}

		for i, matcher := range rule.MatchCommandElements {
			switch matcher {
			case "*":
				// We have already validated that there is an element of the command
				// array at this position.
				continue

			case "**":
				// 'Exit early', because we want to match on anything (or nothing)
				// subsequent to this.
				return rule, nil

			default:
				// Treat everything else as an exact string match.
				if command[i] == matcher {
					continue
				}
			}

			// If we didn't match anything for this element then move onto the next rule
			continue matchRule
		}

		// If we're at the end of this rule and we haven't broken out or returned
		// then we've fully matched the command.
		return rule, nil
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

// Validate checks the console template object for correctness and returns a
// list of errors.
func (ct *ConsoleTemplate) Validate() error {
	var err error

	for i, rule := range ct.Spec.AuthorisationRules {
		for j, element := range rule.MatchCommandElements {
			switch element {
			case "":
				err = multierror.Append(err, errors.Errorf(
					".spec.authorisationRules[%d].matchCommandElements[%d]: an empty matcher is invalid",
					i, j,
				))

			case "**":
				// By only supporting double wildcards at the end we keep the logic
				// simple, as there's no need to backtrack.
				if (j + 1) < len(rule.MatchCommandElements) {
					err = multierror.Append(err, errors.Errorf(
						".spec.authorisationRules[%d].matchCommandElements[%d]: a double wildcard is only valid at the end of the pattern",
						i, j,
					))
				}
			}
		}
	}

	if len(ct.Spec.AuthorisationRules) > 0 && ct.Spec.DefaultAuthorisationRule == nil {
		err = multierror.Append(err, errors.New(
			".spec.defaultAuthorisationRule must be set if authorisation rules are defined",
		))
	}

	return err
}
