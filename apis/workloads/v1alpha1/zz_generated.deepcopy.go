//go:build !ignore_autogenerated

// Code generated by controller-gen. DO NOT EDIT.

package v1alpha1

import (
	"k8s.io/api/rbac/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *Console) DeepCopyInto(out *Console) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new Console.
func (in *Console) DeepCopy() *Console {
	if in == nil {
		return nil
	}
	out := new(Console)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *Console) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleAuthorisation) DeepCopyInto(out *ConsoleAuthorisation) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleAuthorisation.
func (in *ConsoleAuthorisation) DeepCopy() *ConsoleAuthorisation {
	if in == nil {
		return nil
	}
	out := new(ConsoleAuthorisation)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ConsoleAuthorisation) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleAuthorisationList) DeepCopyInto(out *ConsoleAuthorisationList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ConsoleAuthorisation, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleAuthorisationList.
func (in *ConsoleAuthorisationList) DeepCopy() *ConsoleAuthorisationList {
	if in == nil {
		return nil
	}
	out := new(ConsoleAuthorisationList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ConsoleAuthorisationList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleAuthorisationRule) DeepCopyInto(out *ConsoleAuthorisationRule) {
	*out = *in
	if in.MatchCommandElements != nil {
		in, out := &in.MatchCommandElements, &out.MatchCommandElements
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
	in.ConsoleAuthorisers.DeepCopyInto(&out.ConsoleAuthorisers)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleAuthorisationRule.
func (in *ConsoleAuthorisationRule) DeepCopy() *ConsoleAuthorisationRule {
	if in == nil {
		return nil
	}
	out := new(ConsoleAuthorisationRule)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleAuthorisationSpec) DeepCopyInto(out *ConsoleAuthorisationSpec) {
	*out = *in
	out.ConsoleRef = in.ConsoleRef
	if in.Authorisations != nil {
		in, out := &in.Authorisations, &out.Authorisations
		*out = make([]v1.Subject, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleAuthorisationSpec.
func (in *ConsoleAuthorisationSpec) DeepCopy() *ConsoleAuthorisationSpec {
	if in == nil {
		return nil
	}
	out := new(ConsoleAuthorisationSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleAuthorisationStatus) DeepCopyInto(out *ConsoleAuthorisationStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleAuthorisationStatus.
func (in *ConsoleAuthorisationStatus) DeepCopy() *ConsoleAuthorisationStatus {
	if in == nil {
		return nil
	}
	out := new(ConsoleAuthorisationStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleAuthorisationUpdate) DeepCopyInto(out *ConsoleAuthorisationUpdate) {
	*out = *in
	if in.existingAuth != nil {
		in, out := &in.existingAuth, &out.existingAuth
		*out = new(ConsoleAuthorisation)
		(*in).DeepCopyInto(*out)
	}
	if in.updatedAuth != nil {
		in, out := &in.updatedAuth, &out.updatedAuth
		*out = new(ConsoleAuthorisation)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleAuthorisationUpdate.
func (in *ConsoleAuthorisationUpdate) DeepCopy() *ConsoleAuthorisationUpdate {
	if in == nil {
		return nil
	}
	out := new(ConsoleAuthorisationUpdate)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleAuthorisers) DeepCopyInto(out *ConsoleAuthorisers) {
	*out = *in
	if in.Subjects != nil {
		in, out := &in.Subjects, &out.Subjects
		*out = make([]v1.Subject, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleAuthorisers.
func (in *ConsoleAuthorisers) DeepCopy() *ConsoleAuthorisers {
	if in == nil {
		return nil
	}
	out := new(ConsoleAuthorisers)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleList) DeepCopyInto(out *ConsoleList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]Console, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleList.
func (in *ConsoleList) DeepCopy() *ConsoleList {
	if in == nil {
		return nil
	}
	out := new(ConsoleList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ConsoleList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleSpec) DeepCopyInto(out *ConsoleSpec) {
	*out = *in
	out.ConsoleTemplateRef = in.ConsoleTemplateRef
	if in.TTLSecondsBeforeRunning != nil {
		in, out := &in.TTLSecondsBeforeRunning, &out.TTLSecondsBeforeRunning
		*out = new(int32)
		**out = **in
	}
	if in.TTLSecondsAfterFinished != nil {
		in, out := &in.TTLSecondsAfterFinished, &out.TTLSecondsAfterFinished
		*out = new(int32)
		**out = **in
	}
	if in.Command != nil {
		in, out := &in.Command, &out.Command
		*out = make([]string, len(*in))
		copy(*out, *in)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleSpec.
func (in *ConsoleSpec) DeepCopy() *ConsoleSpec {
	if in == nil {
		return nil
	}
	out := new(ConsoleSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleStatus) DeepCopyInto(out *ConsoleStatus) {
	*out = *in
	if in.ExpiryTime != nil {
		in, out := &in.ExpiryTime, &out.ExpiryTime
		*out = (*in).DeepCopy()
	}
	if in.CompletionTime != nil {
		in, out := &in.CompletionTime, &out.CompletionTime
		*out = (*in).DeepCopy()
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleStatus.
func (in *ConsoleStatus) DeepCopy() *ConsoleStatus {
	if in == nil {
		return nil
	}
	out := new(ConsoleStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleTemplate) DeepCopyInto(out *ConsoleTemplate) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleTemplate.
func (in *ConsoleTemplate) DeepCopy() *ConsoleTemplate {
	if in == nil {
		return nil
	}
	out := new(ConsoleTemplate)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ConsoleTemplate) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleTemplateList) DeepCopyInto(out *ConsoleTemplateList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]ConsoleTemplate, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleTemplateList.
func (in *ConsoleTemplateList) DeepCopy() *ConsoleTemplateList {
	if in == nil {
		return nil
	}
	out := new(ConsoleTemplateList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject is an autogenerated deepcopy function, copying the receiver, creating a new runtime.Object.
func (in *ConsoleTemplateList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleTemplateSpec) DeepCopyInto(out *ConsoleTemplateSpec) {
	*out = *in
	in.Template.DeepCopyInto(&out.Template)
	if in.AdditionalAttachSubjects != nil {
		in, out := &in.AdditionalAttachSubjects, &out.AdditionalAttachSubjects
		*out = make([]v1.Subject, len(*in))
		copy(*out, *in)
	}
	if in.DefaultTTLSecondsBeforeRunning != nil {
		in, out := &in.DefaultTTLSecondsBeforeRunning, &out.DefaultTTLSecondsBeforeRunning
		*out = new(int32)
		**out = **in
	}
	if in.DefaultTTLSecondsAfterFinished != nil {
		in, out := &in.DefaultTTLSecondsAfterFinished, &out.DefaultTTLSecondsAfterFinished
		*out = new(int32)
		**out = **in
	}
	if in.AuthorisationRules != nil {
		in, out := &in.AuthorisationRules, &out.AuthorisationRules
		*out = make([]ConsoleAuthorisationRule, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
	if in.DefaultAuthorisationRule != nil {
		in, out := &in.DefaultAuthorisationRule, &out.DefaultAuthorisationRule
		*out = new(ConsoleAuthorisers)
		(*in).DeepCopyInto(*out)
	}
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleTemplateSpec.
func (in *ConsoleTemplateSpec) DeepCopy() *ConsoleTemplateSpec {
	if in == nil {
		return nil
	}
	out := new(ConsoleTemplateSpec)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *ConsoleTemplateStatus) DeepCopyInto(out *ConsoleTemplateStatus) {
	*out = *in
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new ConsoleTemplateStatus.
func (in *ConsoleTemplateStatus) DeepCopy() *ConsoleTemplateStatus {
	if in == nil {
		return nil
	}
	out := new(ConsoleTemplateStatus)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto is an autogenerated deepcopy function, copying the receiver, writing into out. in must be non-nil.
func (in *PodTemplatePreserveMetadataSpec) DeepCopyInto(out *PodTemplatePreserveMetadataSpec) {
	*out = *in
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopy is an autogenerated deepcopy function, copying the receiver, creating a new PodTemplatePreserveMetadataSpec.
func (in *PodTemplatePreserveMetadataSpec) DeepCopy() *PodTemplatePreserveMetadataSpec {
	if in == nil {
		return nil
	}
	out := new(PodTemplatePreserveMetadataSpec)
	in.DeepCopyInto(out)
	return out
}
