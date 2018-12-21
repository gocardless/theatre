package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/directory/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	clientset "github.com/lawrencejones/rbac-directory/pkg/client/clientset/versioned"
	informers "github.com/lawrencejones/rbac-directory/pkg/client/informers/externalversions"
	"github.com/lawrencejones/rbac-directory/pkg/controller"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
)

var (
	app     = kingpin.New("rbac-directory", "Kubernetes operator to manage syncing RBAC").Version(Version)
	logger  = kitlog.NewLogfmtLogger(os.Stderr)
	subject = app.Flag("subject", "Service Subject account").Default("robot-admin@gocardless.com").String()

	membership     = app.Command("membership", "List groups for which user is a member")
	membershipUser = membership.Arg("user", "Users email address").Required().String()

	members      = app.Command("members", "List all users in group")
	membersGroup = members.Arg("group", "Group to list users for").Required().String()

	operate                = app.Command("operate", "Operate on kubernetes 😷")
	operateContext         = operate.Flag("context", "Kubernetes cluster context").Default("lab").String()
	operateRefreshInterval = operate.Flag("refresh-interval", "Period to refresh our listeners").Default("10s").Duration()

	// Version is set by goreleaser
	Version = "dev"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	parsed := kingpin.MustParse(app.Parse(os.Args[1:]))
	client, err := createAdminClient(ctx, *subject)
	if err != nil {
		app.Fatalf("failed to create admin client: %v", err)
	}

	switch parsed {
	case membership.FullCommand():
		resp, err := client.Groups.List().UserKey(*membershipUser).Do()
		if err != nil {
			app.Fatalf("failed to load groups: %v", err)
		}

		for _, group := range resp.Groups {
			fmt.Println(group.Email)
		}

	case members.FullCommand():
		resp, err := client.Members.List(*membersGroup).Do()
		if err != nil {
			app.Fatalf("failed to load members: %v", err)
		}

		for _, member := range resp.Members {
			fmt.Println(member.Email)
		}

	case operate.FullCommand():
		sigc := make(chan os.Signal, 1)
		signal.Notify(sigc, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)

		go func() {
			if recv := <-sigc; recv != nil {
				logger.Log("event", "context.cancel", "msg", "received signal, closing")
				cancel()
				close(sigc)
			}
		}()

		config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			clientcmd.NewDefaultClientConfigLoadingRules(),
			&clientcmd.ConfigOverrides{CurrentContext: *operateContext},
		).ClientConfig()

		if err != nil {
			app.Fatalf("failed to identify kubernetes config: %v", err)
		}

		kubeclientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			app.Fatalf("failed to connect to kubernetes: %v", err)
		}

		clientset, err := clientset.NewForConfig(config)
		if err != nil {
			app.Fatalf("failed to create kubernetes config: %v", err)
		}

		rbacInformerFactory := informers.
			NewSharedInformerFactory(clientset, *operateRefreshInterval)

		ctrl := controller.NewController(
			ctx,
			logger,
			kubeclientset,
			clientset,
			rbacInformerFactory.Rbac().V1alpha1().DirectoryRoleBindings(),
		)

		fmt.Println(ctrl)
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
