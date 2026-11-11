// Package hubstate stores the single-writer Hub directory and independent bearer
// credentials. It does not authorize HTTP requests or manage live Hub sessions.
package hubstate

import "time"

const LocalMemberID = "local"

type Error struct {
	Code string
}

func (e *Error) Error() string { return "hub state: " + e.Code }

func failure(code string) error { return &Error{Code: code} }

type ClusterInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Environment string `json:"environment"`
	Region      string `json:"region"`
}

type Cluster struct {
	ClusterInput
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

type IssuedAgent struct {
	Cluster Cluster `json:"cluster"`
	Token   string  `json:"token"`
}

type GroupInput struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ClusterIDs []string `json:"clusterIds"`
}

type Group struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	ClusterIDs []string  `json:"clusterIds"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type ConnectionSettings struct {
	LANURL    string `json:"lanURL"`
	PublicURL string `json:"publicURL"`
}

type Member struct {
	ID               string    `json:"id"`
	UserID           string    `json:"userId"`
	DisplayName      string    `json:"displayName"`
	Role             string    `json:"role"`
	Status           string    `json:"status"`
	ClusterIDs       []string  `json:"clusterIds"`
	KubernetesGroups []string  `json:"kubernetesGroups"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type MemberInput struct {
	Role             string
	Status           string
	ClusterIDs       []string
	KubernetesGroups []string
}

type Credential struct {
	ID           string     `json:"id"`
	OwnerID      string     `json:"ownerId"`
	Label        string     `json:"label"`
	ClusterIDs   []string   `json:"clusterIds"`
	ReadOnly     bool       `json:"readOnly"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    time.Time  `json:"expiresAt"`
	RefreshUntil time.Time  `json:"refreshUntil"`
	RevokedAt    *time.Time `json:"revokedAt,omitempty"`
}

type IssueInput struct {
	Label      string   `json:"label"`
	ClusterIDs []string `json:"clusterIds"`
	ReadOnly   bool     `json:"readOnly"`
	TTLSeconds int      `json:"ttlSeconds"`
}

type IssuedCredential struct {
	Credential Credential `json:"credential"`
	Token      string     `json:"token"`
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	copy := *t
	return &copy
}

func cloneCluster(c Cluster) Cluster {
	c.RevokedAt = cloneTime(c.RevokedAt)
	return c
}

func cloneCredential(c Credential) Credential {
	c.ClusterIDs = append([]string{}, c.ClusterIDs...)
	c.RevokedAt = cloneTime(c.RevokedAt)
	return c
}

func cloneGroup(g Group) Group {
	g.ClusterIDs = append([]string{}, g.ClusterIDs...)
	return g
}

func cloneMember(m Member) Member {
	m.ClusterIDs = append([]string{}, m.ClusterIDs...)
	m.KubernetesGroups = append([]string{}, m.KubernetesGroups...)
	return m
}

func cloneConnectionSettings(settings *ConnectionSettings) *ConnectionSettings {
	if settings == nil {
		return &ConnectionSettings{}
	}
	copy := *settings
	return &copy
}
