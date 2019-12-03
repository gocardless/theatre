package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/alecthomas/kingpin"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP

	"github.com/gocardless/theatre/cmd"
	"github.com/gocardless/theatre/pkg/signals"
	"github.com/gocardless/theatre/pkg/vault/envconsul"
)

var (
	app                     = kingpin.New("vault-webhook-server", "").Version(Version)
	theatreImage            = app.Flag("theatre-image", "Set to the same image as current binary").Required().String()
	installPath             = app.Flag("install-path", "Location to install theatre binaries").Default("/var/run/theatre").String()
	vaultConfigMapName      = app.Flag("vault-configmap-name", "Vault configMap name containing vault configuration").Default("vault-config").String()
	vaultConfigMapNamespace = app.Flag("vault-configmap-namespace", "Namespace of vault configMap").Default("vault-system").String()
	tlsCertFile             = app.Flag("tls-cert-file", "Mount path for x509 Certificate file").Default("/var/run/certs/cert.pem").String()
	tlsKeyFile              = app.Flag("tls-key-file", "Mount path for x509 Key file").Default("/var/run/certs/cert.pem").String()

	commonOpts = cmd.NewCommonOptions(app)

	// Version is set at compile time
	Version = "dev"
)

func main() {
	kingpin.MustParse(app.Parse(os.Args[1:]))
	logger := commonOpts.Logger()

	ctx, cancel := signals.SetupSignalHandler()
	defer cancel()

	tlsCertPair, err := tls.LoadX509KeyPair(*tlsCertFile, *tlsKeyFile)
	if err != nil {
		app.Fatalf("failed to load key pair: %v", err)
	}

	injectorOpts := envconsul.InjectorOptions{
		Image:                   *theatreImage,
		InstallPath:             *installPath,
		VaultConfigMapNamespace: *vaultConfigMapNamespace,
		VaultConfigMapName:      *vaultConfigMapName,
	}

	wh, err := envconsul.NewWebhook(logger, injectorOpts)
	if err != nil {
		app.Fatalf("failed to create webhook: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/mutate-pods", wh.Handle)

	server := &http.Server{
		Addr:      fmt.Sprintf(":%v", 443),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{tlsCertPair}},
		Handler:   mux,
	}

	go func() {
		if err := server.ListenAndServeTLS("", ""); err != nil {
			app.Fatalf("failed to listen and serve webhook server: %v", err)
		}
	}()

	logger.Log("event", "server.started")

	<-ctx.Done()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		app.Fatalf("failed to gracefully shutdown the webhook server: %v", err)
	}

	logger.Log("event", "server.stopped")
}
