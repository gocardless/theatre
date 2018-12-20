package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/kingpin"
	kitlog "github.com/go-kit/kit/log"
	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/directory/v1"
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
