package v1alpha1

func (r *Release) IsStatusInitialised() bool {
	return r.Status.ObservedGeneration > 0
}
