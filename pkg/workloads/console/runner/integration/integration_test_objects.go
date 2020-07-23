package integration

import (
	workloadsv1alpha1 "github.com/gocardless/theatre/apis/workloads/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExampleRoleBinding is an example of a RoleBinding for use in a test
var ExampleRoleBinding = &rbacv1.RoleBinding{
	ObjectMeta: metav1.ObjectMeta{
		Namespace: "example-ns",
		Name:      "example-name",
	},
	Subjects: []rbacv1.Subject{
		{
			Kind: "User",
			Name: "example-username",
		},
	},
	RoleRef: rbacv1.RoleRef{
		Kind: "Role",
		Name: "console-test",
	},
}

// ExampleConsole is an example of a Console for use in a test
var ExampleConsole = &workloadsv1alpha1.Console{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "example-name",
		Namespace: "example-ns",
	},
	Spec: workloadsv1alpha1.ConsoleSpec{
		User: "example-username",
		ConsoleTemplateRef: corev1.LocalObjectReference{
			Name: "example-consoleTemplateName",
		},
	},
}

// ExampleConsoleTemplate is an example of a ConsoleTemplate for use in a test
var ExampleConsoleTemplate = &workloadsv1alpha1.ConsoleTemplate{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "example-name",
		Namespace: "example-ns",
	},
	Spec: workloadsv1alpha1.ConsoleTemplateSpec{
		Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Image: "alpine:latest",
						Name:  "console-container-0",
					},
				},
			},
		},
	},
}

// ExampleNamespace is an example of a namespace for use in a test
var ExampleNamespace = &corev1.Namespace{
	ObjectMeta: metav1.ObjectMeta{
		Name: "example-name",
	},
}
