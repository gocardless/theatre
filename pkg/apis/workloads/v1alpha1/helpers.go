package v1alpha1

import "time"

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

func (ct *ConsoleTemplate) HasAuthorisationRules() bool {
	if len(ct.Spec.AuthorisationRules) > 0 || ct.Spec.DefaultAuthorisationRule != nil {
		return true
	}

	return false
}
