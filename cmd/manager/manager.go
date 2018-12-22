package main

import (
	"context"
	stdlog "log"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	level "github.com/go-kit/kit/log/level"

	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/directory/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	"k8s.io/klog"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"

	rbacv1alpha1 "github.com/lawrencejones/theatre/pkg/apis/rbac/v1alpha1"
)

var (
	app             = kingpin.New("manager", "Manages GoCardless operators 😷").Version(Version)
	subject         = app.Flag("subject", "Service Subject account").Default("robot-admin@gocardless.com").String()
	kubeContext     = app.Flag("context", "Kubernetes cluster context").Default("lab").String()
	refreshInterval = app.Flag("refresh-interval", "Period to refresh our listeners").Default("10s").Duration()
	threads         = app.Flag("threads", "Number of threads for the operator").Default("2").Int()

	logger = kitlog.NewLogfmtLogger(os.Stderr)

	// Version is set by goreleaser
	Version = "dev"
)

func init() {
	logger = level.NewFilter(logger, level.AllowInfo())
	logger = kitlog.With(logger, "ts", kitlog.DefaultTimestampUTC, "caller", kitlog.DefaultCaller)
	stdlog.SetOutput(kitlog.NewStdlibAdapter(logger))
	klog.SetOutput(kitlog.NewStdlibAdapter(logger))
}

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	if err := rbacv1alpha1.AddToScheme(scheme.Scheme); err != nil {
		app.Fatalf("failed to add rbac scheme: %v", err)
	}

	client, err := client.New(config.GetConfigOrDie(), client.Options{})
	if err != nil {
		app.Fatalf("failed to create kubernetes client: %v", err)
	}

	adminClient, err := createAdminClient(context.TODO(), *subject)
	if err != nil {
		app.Fatalf("failed to create Google Admin client: %v", err)
	}

	mgr, err := builder.SimpleController().
		ForType(&rbacv1alpha1.DirectoryRoleBinding{}).
		Owns(&rbacv1.RoleBinding{}).
		Build(&DirectoryRoleBindingReconciler{
			logger:      logger,
			client:      client,
			adminClient: adminClient,
		})

	if err != nil {
		app.Fatalf("failed to build controller: %v", err)
	}

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		app.Fatalf("failed to run manager: %v", err)
	}
}

func createAdminClient(ctx context.Context, subject string) (*admin.Service, error) {
	scopes := []string{
		admin.AdminDirectoryGroupMemberReadonlyScope,
		admin.AdminDirectoryGroupReadonlyScope,
	}

	creds, err := google.FindDefaultCredentials(ctx, scopes...)
	if err != nil {
		return nil, err
	}

	conf, err := google.JWTConfigFromJSON(creds.JSON, strings.Join(scopes, " "))
	if err != nil {
		return nil, err
	}

	// Access to the directory API must be signed with a Subject to enable domain selection.
	conf.Subject = subject

	return admin.New(conf.Client(ctx))
}

type DirectoryRoleBindingReconciler struct {
	logger      kitlog.Logger
	client      client.Client
	adminClient *admin.Service
}

func (r *DirectoryRoleBindingReconciler) Reconcile(request reconcile.Request) (res reconcile.Result, err error) {
	logger := kitlog.With(r.logger, "request", request)
	logger.Log("event", "reconcile.start")

	defer func() {
		if err != nil {
			logger.Log("event", "reconcile.error", "error", err)
		}
	}()

	drb := &rbacv1alpha1.DirectoryRoleBinding{}
	if err := r.client.Get(context.TODO(), request.NamespacedName, drb); err != nil {
		if errors.IsNotFound(err) {
			r.logger.Log("event", "reconcile.not_found")
			return res, nil
		}

		r.logger.Log("event", "reconcile.error", "error", err)
		return res, err
	}

	rb := &rbacv1.RoleBinding{}
	identifier := types.NamespacedName{Name: drb.Name, Namespace: drb.Namespace}
	err = r.client.Get(context.TODO(), identifier, rb)
	if err != nil && errors.IsNotFound(err) {
		logger.Log("event", "reconcile.create", "msg", "no RoleBinding found, creating")

		rb.ObjectMeta = metav1.ObjectMeta{
			Name:      drb.Name,
			Namespace: drb.Namespace,
		}
		rb.RoleRef = drb.RoleRef
		rb.Subjects = []rbacv1.Subject{}

		if err := controllerutil.SetControllerReference(drb, rb, scheme.Scheme); err != nil {
			return reconcile.Result{}, err
		}

		if err = r.client.Create(context.TODO(), rb); err != nil {
			return reconcile.Result{}, err
		}

		if err = r.client.Get(context.TODO(), identifier, rb); err != nil {
			return reconcile.Result{}, err
		}
	}

	subjects, err := r.resolve(drb.Subjects)
	if err != nil {
		return reconcile.Result{}, err
	}

	add, remove := diff(subjects, rb.Subjects), diff(rb.Subjects, subjects)
	if len(add) > 0 || len(remove) > 0 {
		for _, member := range add {
			logger.Log("event", "member.add", "member", member.Name)
		}

		for _, member := range remove {
			logger.Log("event", "member.remove", "member", member.Name)
		}

		logger.Log("event", "reconcile.update", "msg", "updating RoleBinding subjects")
		rb.Subjects = subjects
		if err := r.client.Update(context.TODO(), rb); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func diff(s1 []rbacv1.Subject, s2 []rbacv1.Subject) []rbacv1.Subject {
	result := make([]rbacv1.Subject, 0)
	for _, s := range s1 {
		if !includesSubject(s2, s) {
			result = append(result, s)
		}
	}

	return result
}

func includesSubject(ss []rbacv1.Subject, s rbacv1.Subject) bool {
	for _, existing := range ss {
		if existing.Kind == s.Kind && existing.Name == s.Name && existing.Namespace == s.Namespace {
			return true
		}
	}

	return false
}

func (r *DirectoryRoleBindingReconciler) membersOf(group string) ([]rbacv1.Subject, error) {
	subjects := make([]rbacv1.Subject, 0)
	resp, err := r.adminClient.Members.List(group).Do()

	if err == nil {
		for _, member := range resp.Members {
			subjects = append(subjects, rbacv1.Subject{
				APIGroup: rbacv1.GroupName,
				Kind:     rbacv1.UserKind,
				Name:     member.Email,
			})
		}
	}

	return subjects, err
}

func (r *DirectoryRoleBindingReconciler) resolve(in []rbacv1.Subject) ([]rbacv1.Subject, error) {
	out := make([]rbacv1.Subject, 0)
	for _, subject := range in {
		switch subject.Kind {
		case "GoogleGroup":
			members, err := r.membersOf(subject.Name)
			if err != nil {
				return nil, err
			}

			// For each of our group members, add them if they weren't already here
			for _, member := range members {
				if !includesSubject(out, member) {
					out = append(out, member)
				}
			}

		default:
			out = append(out, subject)
		}
	}

	return out, nil
}
