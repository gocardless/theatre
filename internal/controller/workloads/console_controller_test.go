package controllers

import (
	"testing"

	rbacv1 "k8s.io/api/rbac/v1"
)

func TestCountDistinctSubjects(t *testing.T) {
	cases := []struct {
		name     string
		subjects []rbacv1.Subject
		want     int
	}{
		{
			name: "distinct usernames",
			subjects: []rbacv1.Subject{
				{Kind: "User", Name: "alice"},
				{Kind: "User", Name: "bob"},
			},
			want: 2,
		},
		{
			name: "exact duplicate subjects collapse to one",
			subjects: []rbacv1.Subject{
				{Kind: "User", Name: "alice"},
				{Kind: "User", Name: "alice"},
			},
			want: 1,
		},
		{
			name: "same username with varying namespace collapses to one",
			subjects: []rbacv1.Subject{
				{Kind: "User", Name: "alice", Namespace: "synthetic-a"},
				{Kind: "User", Name: "alice", Namespace: "synthetic-b"},
				{Kind: "User", Name: "alice", Namespace: "synthetic-c"},
			},
			want: 1,
		},
		{
			name: "same username with varying kind/apiGroup collapses to one",
			subjects: []rbacv1.Subject{
				{Kind: "User", Name: "alice"},
				{Kind: "ServiceAccount", APIGroup: "rbac.authorization.k8s.io", Name: "alice"},
			},
			want: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := countDistinctSubjects(tc.subjects)
			if got != tc.want {
				t.Errorf("countDistinctSubjects(%v) = %d, want %d", tc.subjects, got, tc.want)
			}
		})
	}
}
