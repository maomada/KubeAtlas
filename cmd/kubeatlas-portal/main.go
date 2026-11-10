package main

import (
	"context"
	"encoding/json"
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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
	"github.com/maomada/KubeAtlas/internal/hubmcp"
	"github.com/maomada/KubeAtlas/internal/hubstate"
	"github.com/maomada/KubeAtlas/internal/portal"
	"github.com/maomada/KubeAtlas/internal/portalui"
)

const maxTokenMapBytes = 1 << 20

var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "Print version and exit")
	listen := flag.String("listen", "127.0.0.1:9290", "HTTP listen address")
	dataFile := flag.String("data-file", "./portal-data/registry.json", "Persistent registry JSON file")
	fleetDataFile := flag.String("fleet-data-file", "", "Persistent Fleet state JSON file (default: <data-file directory>/fleet/state.json)")
	hubStateFile := flag.String("hub-state-file", os.Getenv("KUBEATLAS_PORTAL_HUB_STATE_FILE"), "Enable live Hub mode with this persistent state JSON file; no snapshot fallback")
	hubGroups := flag.String("hub-groups", os.Getenv("KUBEATLAS_PORTAL_HUB_GROUPS"), "Comma-separated Kubernetes identity groups for the Portal user in Hub mode; grants still require cluster RBAC")
	oidcIssuer := flag.String("oidc-issuer", os.Getenv("KUBEATLAS_PORTAL_OIDC_ISSUER"), "OIDC issuer URL for Hub SSO")
	oidcClientID := flag.String("oidc-client-id", os.Getenv("KUBEATLAS_PORTAL_OIDC_CLIENT_ID"), "OIDC client ID for Hub SSO")
	oidcRedirectURL := flag.String("oidc-redirect-url", os.Getenv("KUBEATLAS_PORTAL_OIDC_REDIRECT_URL"), "Fixed OIDC callback URL for Hub SSO")
	oidcCACert := flag.String("oidc-ca-cert", os.Getenv("KUBEATLAS_PORTAL_OIDC_CA_CERT"), "Optional CA certificate file for the OIDC issuer")
	insecureCookie := flag.Bool("insecure-cookie", false, "Allow non-Secure viewer cookies for loopback HTTP development only")
	mcpAllowedHosts := flag.String("mcp-allowed-hosts", os.Getenv("KUBEATLAS_PORTAL_MCP_ALLOWED_HOSTS"), "Comma-separated exact external host[:port] authorities allowed for /mcp-fleet behind a trusted proxy; no wildcards")
	flag.Parse()
	if *showVersion {
		fmt.Printf("kubeatlas-portal %s\n", version)
		return
	}
	fleetStateFile := effectiveFleetStatePath(*dataFile, *hubStateFile, *fleetDataFile)
	if err := preflightStatePaths(*dataFile, *hubStateFile, fleetStateFile); err != nil {
		log.Fatalf("portal state path configuration is invalid: %v", err)
	}
	if host, _, err := net.SplitHostPort(*listen); err == nil &&
		host != "localhost" && !net.ParseIP(host).IsLoopback() {
		log.Printf("WARNING: Portal is listening beyond loopback; use only on a trusted isolated development network, never on the public internet or in production")
	}

	credentials, err := loadCredentials(*hubStateFile != "")
	if err != nil {
		log.Fatal(err)
	}
	if err := configureHub(&credentials, *hubStateFile, *hubGroups); err != nil {
		log.Fatalf("portal Hub configuration is invalid: %v", err)
	}
	if err := configureOIDC(&credentials, *oidcIssuer, *oidcClientID, *oidcRedirectURL, *oidcCACert, os.Getenv("KUBEATLAS_PORTAL_OIDC_CLIENT_SECRET")); err != nil {
		log.Fatalf("portal OIDC configuration is invalid: %v", err)
	}
	credentials.AllowInsecureCookies = *insecureCookie
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
	registry, err := portal.OpenRegistry(*dataFile)
	if err != nil {
		log.Fatalf("portal registry startup failed: %v", err)
	}
	var fleetStore *fleetsync.Store
	if fleetStateFile != "" {
		fleetStore, err = fleetsync.OpenStore(fleetStateFile)
		if err != nil {
			log.Fatalf("portal Fleet state startup failed: %v", err)
		}
		defer fleetStore.Close()
	}
	static, err := fs.Sub(portalui.FS, "dist")
	if err != nil {
		log.Fatalf("portal web assets are unavailable; build the portal frontend and embed it")
	}
	handler, err := portal.NewHandler(registry, credentials, static, fleetStore)
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
	log.Printf("KubeAtlas Portal listening on %s", listener.Addr())

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

func loadCredentials(hubMode bool) (portal.Credentials, error) {
	rawTokens := os.Getenv("KUBEATLAS_PORTAL_TOKENS")
	if rawTokens == "" && hubMode {
		rawTokens = "{}"
	}
	if len(rawTokens) > maxTokenMapBytes {
		return portal.Credentials{}, fmt.Errorf("KUBEATLAS_PORTAL_TOKENS exceeds %d bytes", maxTokenMapBytes)
	}
	var tokens map[string]string
	decoder := json.NewDecoder(io.LimitReader(strings.NewReader(rawTokens), maxTokenMapBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&tokens); err != nil || tokens == nil {
		return portal.Credentials{}, errors.New("KUBEATLAS_PORTAL_TOKENS must be a JSON object mapping cluster IDs to registration tokens")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return portal.Credentials{}, errors.New("KUBEATLAS_PORTAL_TOKENS must contain one JSON object")
	}
	return portal.Credentials{
		Tokens: tokens, Username: os.Getenv("KUBEATLAS_PORTAL_USERNAME"),
		Password: os.Getenv("KUBEATLAS_PORTAL_PASSWORD"),
	}, nil
}

func configureHub(credentials *portal.Credentials, stateFile, groups string) error {
	if stateFile == "" {
		if groups != "" {
			return errors.New("--hub-groups requires --hub-state-file")
		}
		return nil
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

func effectiveFleetStatePath(dataFile, hubStateFile, fleetDataFile string) string {
	if fleetDataFile != "" {
		return fleetDataFile
	}
	if hubStateFile != "" {
		return ""
	}
	return filepath.Join(filepath.Dir(dataFile), "fleet", "state.json")
}

func preflightStatePaths(registryPath, hubPath, fleetPath string) error {
	type statePath struct {
		role     string
		path     string
		resolved string
	}
	paths := []statePath{{role: "--data-file", path: registryPath}}
	if hubPath != "" {
		paths = append(paths, statePath{role: "--hub-state-file", path: hubPath})
	}
	if fleetPath != "" {
		paths = append(paths, statePath{role: "--fleet-data-file", path: fleetPath})
	}
	for i := range paths {
		resolved, err := resolveStatePath(paths[i].path)
		if err != nil {
			return fmt.Errorf("cannot resolve %s state path %q: %w", paths[i].role, paths[i].path, err)
		}
		paths[i].resolved = resolved
	}
	for i := 0; i < len(paths); i++ {
		for j := i + 1; j < len(paths); j++ {
			same := paths[i].resolved == paths[j].resolved
			if !same {
				left, leftExists, err := existingStatePath(paths[i].resolved)
				if err != nil {
					return fmt.Errorf("cannot inspect %s state path %q: %w", paths[i].role, paths[i].path, err)
				}
				right, rightExists, err := existingStatePath(paths[j].resolved)
				if err != nil {
					return fmt.Errorf("cannot inspect %s state path %q: %w", paths[j].role, paths[j].path, err)
				}
				same = leftExists && rightExists && os.SameFile(left, right)
			}
			if same {
				return fmt.Errorf("state path collision: %s and %s refer to the same file", paths[i].role, paths[j].role)
			}
		}
	}
	return nil
}

func resolveStatePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is empty")
	}
	path = filepath.FromSlash(filepath.ToSlash(path))
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." {
			return "", errors.New("parent traversal is not permitted in state paths")
		}
	}
	var current string
	var components []string
	if filepath.IsAbs(path) {
		volume := filepath.VolumeName(path)
		remainder := strings.TrimPrefix(path, volume)
		current = volume + string(filepath.Separator)
		components = strings.Split(strings.TrimLeft(remainder, string(filepath.Separator)), string(filepath.Separator))
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		current, err = filepath.EvalSymlinks(cwd)
		if err != nil {
			return "", err
		}
		components = strings.Split(path, string(filepath.Separator))
	}
	for _, component := range components {
		switch component {
		case "", ".":
			continue
		}
		candidate := filepath.Join(current, component)
		info, err := os.Lstat(candidate)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
				current = candidate
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			current, err = filepath.EvalSymlinks(candidate)
			if err != nil {
				return "", err
			}
			current, err = filepath.Abs(current)
			if err != nil {
				return "", err
			}
			continue
		}
		current = candidate
	}
	return filepath.Clean(current), nil
}

func existingStatePath(path string) (os.FileInfo, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return info, true, nil
}
