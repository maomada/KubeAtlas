package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/maomada/KubeAtlas/internal/hubartifacts"
	"github.com/maomada/KubeAtlas/internal/hubmcp"
	"github.com/maomada/KubeAtlas/internal/hubstate"
	"github.com/maomada/KubeAtlas/internal/portal"
	"github.com/maomada/KubeAtlas/internal/portalui"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	listen := flag.String("listen", "127.0.0.1:9290", "HTTP listen address")
	hubStateFile := flag.String("hub-state-file", os.Getenv("KUBEATLAS_PORTAL_HUB_STATE_FILE"), "Required persistent state file for the self-hosted Cloud Hub")
	hubArtifactsDir := flag.String("hub-artifacts-dir", os.Getenv("KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR"), "Optional read-only directory containing the version-matched Hub Agent artifact bundle")
	previousArtifactsDir := flag.String("hub-previous-artifacts-dir", "", "Optional read-only previous Hub artifact bundle, retained for digest-only image pulls")
	previousArtifactsVersion := flag.String("hub-previous-artifacts-version", "", "Exact build version of the previous Hub Agent artifact bundle")
	hubGroups := flag.String("hub-groups", os.Getenv("KUBEATLAS_PORTAL_HUB_GROUPS"), "Comma-separated Kubernetes identity groups for the local Cloud Hub owner; grants still require cluster RBAC")
	oidcIssuer := flag.String("oidc-issuer", os.Getenv("KUBEATLAS_PORTAL_OIDC_ISSUER"), "OIDC issuer URL for Hub SSO")
	oidcClientID := flag.String("oidc-client-id", os.Getenv("KUBEATLAS_PORTAL_OIDC_CLIENT_ID"), "OIDC client ID for Hub SSO")
	oidcRedirectURL := flag.String("oidc-redirect-url", os.Getenv("KUBEATLAS_PORTAL_OIDC_REDIRECT_URL"), "Fixed OIDC callback URL for Hub SSO")
	oidcCACert := flag.String("oidc-ca-cert", os.Getenv("KUBEATLAS_PORTAL_OIDC_CA_CERT"), "Optional CA certificate file for the OIDC issuer")
	insecureCookie := flag.Bool("insecure-cookie", false, "Allow non-Secure viewer cookies for loopback HTTP development only")
	httpIPOrigin := registerHTTPIPOriginFlag(flag.CommandLine, os.Getenv)
	mcpAllowedHosts := flag.String("mcp-allowed-hosts", os.Getenv("KUBEATLAS_PORTAL_MCP_ALLOWED_HOSTS"), "Comma-separated exact external host[:port] authorities allowed for /mcp behind a trusted proxy; no wildcards")
	flag.Parse()
	if *showVersion {
		fmt.Printf("kubeatlas-portal %s\n", version)
		return
	}
	if strings.TrimSpace(*hubStateFile) == "" {
		log.Fatal("--hub-state-file is required; set KUBEATLAS_PORTAL_HUB_STATE_FILE or pass --hub-state-file")
	}
	if host, _, err := net.SplitHostPort(*listen); err == nil &&
		host != "localhost" && !net.ParseIP(host).IsLoopback() {
		log.Printf("WARNING: self-hosted Cloud Hub is listening beyond loopback; put it behind a trusted TLS-terminating proxy before exposing it")
	}

	credentials := loadCredentials()
	if err := configureHub(&credentials, *hubStateFile, *hubGroups); err != nil {
		log.Fatalf("portal Hub configuration is invalid: %v", err)
	}
	if err := configureArtifacts(&credentials, *hubArtifactsDir, version); err != nil {
		log.Fatalf("portal Hub artifact catalog is invalid: %v", err)
	}
	if *previousArtifactsDir != "" || *previousArtifactsVersion != "" {
		if *previousArtifactsDir == "" || *previousArtifactsVersion == "" || credentials.Hub.Artifacts == nil {
			log.Fatal("retaining previous images requires an active and a fully specified previous catalog")
		}
		previous, err := hubartifacts.Open(*previousArtifactsDir, *previousArtifactsVersion)
		if err != nil {
			log.Fatalf("previous Hub artifact catalog is invalid: %v", err)
		}
		defer previous.Close()
		credentials.Hub.Artifacts.WithPreviousImages(previous)
	}
	if err := configureOIDC(&credentials, *oidcIssuer, *oidcClientID, *oidcRedirectURL, *oidcCACert, os.Getenv("KUBEATLAS_PORTAL_OIDC_CLIENT_SECRET")); err != nil {
		log.Fatalf("portal OIDC configuration is invalid: %v", err)
	}
	credentials.AllowInsecureCookies = *insecureCookie
	credentials.HTTPIPOrigin = *httpIPOrigin
	if strings.TrimSpace(*mcpAllowedHosts) != "" {
		for _, host := range strings.Split(*mcpAllowedHosts, ",") {
			credentials.MCPAllowedHosts = append(credentials.MCPAllowedHosts, strings.TrimSpace(host))
		}
	}
	if *insecureCookie {
		host, _, err := net.SplitHostPort(*listen)
		if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
			log.Fatal("--insecure-cookie requires a loopback listen address")
		}
	}
	static, err := fs.Sub(portalui.FS, "dist")
	if err != nil {
		log.Fatalf("portal web assets are unavailable; build the portal frontend and embed it")
	}
	handler, err := portal.NewHandler(credentials, static)
	if err != nil {
		log.Fatalf("portal configuration is invalid: %v", err)
	}
	defer handler.Close()

	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      11 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    10 << 10,
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("portal listen failed: %v", err)
	}
	log.Printf("KubeAtlas self-hosted Cloud Hub listening on %s", listener.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("portal shutdown failed: %v", err)
		}
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("portal server failed: %v", err)
		}
	}
}

func registerHTTPIPOriginFlag(flags *flag.FlagSet, getenv func(string) string) *string {
	return flags.String("http-ip-origin", getenv("KUBEATLAS_PORTAL_HTTP_IP_ORIGIN"), "Explicit trusted-network HTTP root origin for a literal IP authority; enables non-Secure cookies only for that exact authority")
}

func configureArtifacts(credentials *portal.Credentials, directory, buildVersion string) error {
	if credentials.Hub == nil {
		return errors.New("Hub artifact catalog requires Hub configuration")
	}
	catalog, err := hubartifacts.Open(directory, buildVersion)
	if err != nil {
		return err
	}
	credentials.Hub.Artifacts = catalog
	return nil
}

func loadCredentials() portal.Credentials {
	return portal.Credentials{
		Username: os.Getenv("KUBEATLAS_PORTAL_USERNAME"),
		Password: os.Getenv("KUBEATLAS_PORTAL_PASSWORD"),
	}
}

func configureHub(credentials *portal.Credentials, stateFile, groups string) error {
	if strings.TrimSpace(stateFile) == "" {
		return errors.New("--hub-state-file is required")
	}
	store, err := hubstate.OpenStore(stateFile)
	if err != nil {
		return err
	}
	config := &portal.HubConfig{Store: store, MCPFactory: hubmcp.New}
	if groups != "" {
		for _, group := range strings.Split(groups, ",") {
			config.Groups = append(config.Groups, strings.TrimSpace(group))
		}
	}
	credentials.Hub = config
	return nil
}

func configureOIDC(credentials *portal.Credentials, issuer, clientID, redirectURL, caCertFile, clientSecret string) error {
	configured := issuer != "" || clientID != "" || redirectURL != "" || caCertFile != "" || clientSecret != ""
	if !configured {
		return nil
	}
	if credentials.Hub == nil {
		return errors.New("OIDC requires --hub-state-file")
	}
	if strings.TrimSpace(issuer) == "" || strings.TrimSpace(clientID) == "" ||
		strings.TrimSpace(redirectURL) == "" || strings.TrimSpace(clientSecret) == "" {
		return errors.New("OIDC issuer, client ID, redirect URL, and client secret must be configured together")
	}
	credentials.OIDC = &portal.OIDCConfig{
		Issuer: issuer, ClientID: clientID, RedirectURL: redirectURL,
		CACertFile: caCertFile, ClientSecret: clientSecret,
	}
	return nil
}
