package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/maomada/KubeAtlas/internal/hubartifacts"
	"github.com/maomada/KubeAtlas/internal/hubmcp"
	"github.com/maomada/KubeAtlas/internal/hubstate"
	"github.com/maomada/KubeAtlas/internal/portal"
	"github.com/maomada/KubeAtlas/internal/portalui"
)

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	listen := flag.String("listen", "127.0.0.1:1333", "HTTP listen address")
	port := flag.Int("port", 1333, "Override the port in --listen")
	adminUsername := flag.String("admin-username", "", "Local Hub administrator username for initialization or reset")
	adminPasswordFile := flag.String("admin-password-file", "", "File containing the local Hub administrator password")
	resetAdmin := flag.Bool("reset-admin", false, "Explicitly replace the local Hub administrator credentials")
	showSetupCode := flag.Bool("show-setup-code", false, "Print the Hub's first-time setup code locally and exit")
	hubStateFile := flag.String("hub-state-file", os.Getenv("KUBEATLAS_PORTAL_HUB_STATE_FILE"), "Required persistent state file for the self-hosted Cloud Hub")
	hubOrigin := flag.String("hub-origin", os.Getenv("KUBEATLAS_PORTAL_HUB_ORIGIN"), "Initial HTTPS root address for new Hub state")
	hubArtifactsDir := flag.String("hub-artifacts-dir", os.Getenv("KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR"), "Optional read-only directory containing the version-matched Hub Agent artifact bundle")
	checkHubArtifacts := flag.Bool("check-hub-artifacts", false, "Validate the supplied Hub artifact directory against this Portal build and exit")
	importHubArtifacts := flag.String("import-hub-artifacts", "", "Import a verified Hub Agent bundle archive into a new --hub-artifacts-dir and exit")
	hubArtifactsSHA256 := flag.String("hub-artifacts-sha256", "", "Expected SHA-256 checksum of the archive to import")
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
	if *checkHubArtifacts || *importHubArtifacts != "" || *hubArtifactsSHA256 != "" {
		if err := manageHubArtifacts(*hubArtifactsDir, *importHubArtifacts, *hubArtifactsSHA256, *checkHubArtifacts); err != nil {
			log.Fatalf("portal Hub artifact validation failed: %v", err)
		}
		fmt.Printf("Verified Hub Agent artifact catalog for %s\n", version)
		return
	}
	if strings.TrimSpace(*hubStateFile) == "" {
		log.Fatal("--hub-state-file is required; set KUBEATLAS_PORTAL_HUB_STATE_FILE or pass --hub-state-file")
	}
	if *showSetupCode {
		code, err := portal.ReadSetupCode(*hubStateFile)
		if err != nil {
			log.Fatalf("Hub setup code is unavailable: %v", err)
		}
		fmt.Println(code)
		return
	}
	var portOverride bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portOverride = true
		}
	})
	address, err := resolveListenAddress(*listen, *port, portOverride)
	if err != nil {
		log.Fatalf("portal listen address is invalid: %v", err)
	}
	if host, _, err := net.SplitHostPort(address); err == nil &&
		host != "localhost" && !net.ParseIP(host).IsLoopback() {
		log.Printf("WARNING: self-hosted Cloud Hub is listening beyond loopback; put it behind a trusted TLS-terminating proxy before exposing it")
	}

	credentials := loadCredentials()
	if err := configureAdmin(&credentials, *adminUsername, *adminPasswordFile, *resetAdmin); err != nil {
		log.Fatalf("portal administrator configuration is invalid: %v", err)
	}
	if err := configureHub(&credentials, *hubStateFile, *hubGroups, *hubOrigin); err != nil {
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
		host, _, err := net.SplitHostPort(address)
		if err != nil || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
			log.Fatal("--insecure-cookie requires a loopback listen address")
		}
	}
	static, err := fs.Sub(portalui.FS, "dist")
	if err != nil {
		log.Fatalf("portal web assets are unavailable; build the portal frontend and embed it")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("portal listen failed: %v", err)
	}
	defer listener.Close()
	handler, err := portal.NewStartupHandler(credentials, static, *hubStateFile, *resetAdmin)
	if err != nil {
		log.Fatalf("portal configuration is invalid: %v", err)
	}
	defer handler.Close()

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      11 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    10 << 10,
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

func resolveListenAddress(listen string, port int, override bool) (string, error) {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	if !override {
		return listen, nil
	}
	if port < 1 || port > 65535 {
		return "", errors.New("--port must be between 1 and 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func configureAdmin(credentials *portal.Credentials, username, passwordPath string, reset bool) error {
	if username == "" && passwordPath == "" && !reset {
		return nil
	}
	if username == "" || passwordPath == "" {
		return errors.New("--admin-username and --admin-password-file must be provided together, including for --reset-admin")
	}
	info, err := os.Stat(passwordPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("administrator password file must be a readable regular file")
	}
	file, err := os.Open(passwordPath)
	if err != nil {
		return errors.New("could not open administrator password file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 4099))
	if err != nil || len(data) > 4098 {
		return errors.New("could not read administrator password file or file is too large")
	}
	password := string(data)
	if strings.HasSuffix(password, "\n") {
		password = strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r")
	}
	if !utf8.ValidString(password) || len(password) < 16 || len(password) > 4096 {
		return errors.New("administrator password must contain 16-4096 UTF-8 bytes")
	}
	credentials.Username, credentials.Password = username, password
	return nil
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

func manageHubArtifacts(directory, archive, checksum string, check bool) error {
	if directory == "" {
		return errors.New("--hub-artifacts-dir is required")
	}
	if check {
		if archive != "" || checksum != "" {
			return errors.New("--check-hub-artifacts cannot be combined with import options")
		}
		catalog, err := hubartifacts.Open(directory, version)
		if err != nil {
			return err
		}
		return catalog.Close()
	}
	if archive == "" {
		return errors.New("--import-hub-artifacts and --hub-artifacts-sha256 are required together")
	}
	return hubartifacts.ImportArchive(archive, checksum, directory, version)
}

func loadCredentials() portal.Credentials {
	return portal.Credentials{
		Username: os.Getenv("KUBEATLAS_PORTAL_USERNAME"),
		Password: os.Getenv("KUBEATLAS_PORTAL_PASSWORD"),
	}
}

func configureHub(credentials *portal.Credentials, stateFile, groups, origin string) error {
	if strings.TrimSpace(stateFile) == "" {
		return errors.New("--hub-state-file is required")
	}
	store, err := hubstate.OpenStoreWithInitialOrigin(stateFile, origin)
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
