package rbac

import (
	rbacv1 "k8s.io/api/rbac/v1"
)

// Diff finds the subjects that are present in s1 that were not present in s2
func Diff(s1 []rbacv1.Subject, s2 []rbacv1.Subject) []rbacv1.Subject {
	result := make([]rbacv1.Subject, 0)
	for _, s := range s1 {
		if !IncludesSubject(s2, s) {
			result = append(result, s)
		}
	}

	return result
}

// IncludesSubject returns true if a subject s is contained within subjects ss
func IncludesSubject(ss []rbacv1.Subject, s rbacv1.Subject) bool {
	for _, existing := range ss {
		if existing.Kind == s.Kind && existing.Name == s.Name && existing.Namespace == s.Namespace {
			return true
		}
	}

	return false
}
