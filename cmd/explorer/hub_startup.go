package main

import (
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maomada/KubeAtlas/internal/cloud"
	pkgauth "github.com/maomada/KubeAtlas/pkg/auth"
)

const maxHubTokenBytes = 4096

var hubClusterIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type hubStartupEnvironment struct {
	URL       string
	ClusterID string
	TokenFile string
	CAFile    string
}

func hubStartupEnvironmentDefaults() hubStartupEnvironment {
	return hubStartupEnvironment{
		URL:       os.Getenv("KUBEATLAS_AGENT_HUB_URL"),
		ClusterID: os.Getenv("KUBEATLAS_AGENT_HUB_CLUSTER_ID"),
		TokenFile: os.Getenv("KUBEATLAS_AGENT_HUB_TOKEN_FILE"),
		CAFile:    os.Getenv("KUBEATLAS_AGENT_HUB_CA_FILE"),
	}
}

type hubStartupFlags struct {
	URL                   string
	ClusterID             string
	TokenFile             string
	CAFile                string
	LegacyCloudConfigured bool
	AuthMode              string
	CredentialMode        string
	BasePath              string
	MCPEnabled            bool
	MCPCatalogOnly        bool
	MCPCatalogStdio       bool
}

type hubStartup struct {
	Enabled      bool
	Origin       string
	AgentURL     string
	ClusterID    string
	Token        string
	CA           *x509.CertPool
	AuthMode     string
	UserHeader   string
	GroupsHeader string
}

func resolveHubStartup(f hubStartupFlags) (hubStartup, error) {
	var out hubStartup
	configured := f.URL != "" || f.ClusterID != "" || f.TokenFile != "" || f.CAFile != ""
	if !configured {
		return out, nil
	}
	if f.LegacyCloudConfigured {
		return out, errors.New("self-hosted Hub options cannot be combined with legacy Cloud options")
	}
	if f.URL == "" || f.ClusterID == "" || f.TokenFile == "" {
		return out, errors.New("--hub-url, --hub-cluster-id, and --hub-token-file must be configured together")
	}
	if !hubClusterIDPattern.MatchString(f.ClusterID) {
		return out, errors.New("--hub-cluster-id must match ^[a-z0-9][a-z0-9-]{0,62}$")
	}
	if f.BasePath != "" {
		return out, errors.New("--base-path is not supported with the self-hosted Hub agent")
	}
	if !f.MCPEnabled {
		return out, errors.New("the self-hosted Hub agent requires MCP; remove --no-mcp or enable MCP in configuration")
	}
	if f.MCPCatalogOnly || f.MCPCatalogStdio {
		return out, errors.New("catalog-only and catalog-stdio modes cannot be combined with the self-hosted Hub agent")
	}
	if f.AuthMode != "none" && f.AuthMode != "proxy" {
		return out, errors.New("the self-hosted Hub agent requires proxy authentication")
	}
	credentialMode, err := pkgauth.ParseCredentialMode(f.CredentialMode)
	if err != nil {
		return out, fmt.Errorf("--auth-k8s-credentials: %w", err)
	}
	if credentialMode != pkgauth.CredentialsImpersonate {
		return out, errors.New("the self-hosted Hub agent requires Kubernetes impersonation credentials")
	}

	origin, err := cloud.NormalizeHubOrigin(f.URL)
	if err != nil {
		return out, fmt.Errorf("--hub-url: %w", err)
	}
	agentURL, err := hubAgentURL(origin)
	if err != nil {
		return out, fmt.Errorf("--hub-url: %w", err)
	}
	token, err := readHubTokenFile(f.TokenFile)
	if err != nil {
		return out, err
	}
	var ca *x509.CertPool
	if f.CAFile != "" {
		ca, err = cloud.LoadCAFile(f.CAFile)
		if err != nil {
			return out, fmt.Errorf("--hub-ca-file: %w", err)
		}
	}

	return hubStartup{
		Enabled:      true,
		Origin:       origin,
		AgentURL:     agentURL,
		ClusterID:    f.ClusterID,
		Token:        token,
		CA:           ca,
		AuthMode:     "proxy",
		UserHeader:   "X-Forwarded-User",
		GroupsHeader: "X-Forwarded-Groups",
	}, nil
}

func hubAgentURL(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return "", errors.New("invalid Hub origin")
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/agent"
	u.RawPath = ""
	endpoint := u.String()
	if err := cloud.ValidateWebSocketURL(endpoint); err != nil {
		return "", err
	}
	return endpoint, nil
}

func readHubTokenFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot read Hub token file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxHubTokenBytes+1))
	if err != nil {
		return "", errors.New("cannot read Hub token file")
	}
	if len(data) > maxHubTokenBytes {
		return "", errors.New("Hub token file exceeds 4096 bytes")
	}
	token := strings.TrimSpace(string(data))
	if !utf8.ValidString(token) {
		return "", errors.New("Hub token must be valid UTF-8")
	}
	if utf8.RuneCountInString(token) < 32 || len(token) > maxHubTokenBytes {
		return "", errors.New("Hub token must contain at least 32 characters and no more than 4096 bytes")
	}
	if strings.IndexFunc(token, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) >= 0 {
		return "", errors.New("Hub token must not contain embedded whitespace or control characters")
	}
	return token, nil
}
