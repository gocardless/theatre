package main

import (
	"github.com/alecthomas/kingpin"
)

type KubernetesOptions struct {
	Context   string
	Namespace string
}

func (opt *KubernetesOptions) Bind(cmd *kingpin.CmdClause) *KubernetesOptions {
	cmd.Flag("context", "Kubernetes context to target. If not provided defaults to current context").Envar("KUBERNETES_CONTEXT").Default("").StringVar(&opt.Context)
	cmd.Flag("namespace", "Kubernetes namespace to target. If not provided defaults to target allnamespaces").Envar("KUBERNETES_NAMESPACE").Default("").StringVar(&opt.Namespace)
	return opt
}
