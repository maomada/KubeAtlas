package fleetsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultSyncInterval = 30 * time.Second
	minimumSyncInterval = 5 * time.Second
	captureTimeout      = 90 * time.Second
	responseLimit       = 64 << 10
)

type ReporterConfig struct {
	URL       string
	ClusterID string
	Name      string
	Interval  time.Duration
}

type CaptureFunc func(context.Context) (Snapshot, error)

type Reporter struct {
	config  ReporterConfig
	capture CaptureFunc
	client  *http.Client
}

func ConfigFromEnv() (ReporterConfig, bool, error) {
	urlValue := os.Getenv("KUBEATLAS_FLEET_URL")
	clusterID := os.Getenv("KUBEATLAS_FLEET_CLUSTER_ID")
	name := os.Getenv("KUBEATLAS_FLEET_CLUSTER_NAME")
	intervalValue := os.Getenv("KUBEATLAS_FLEET_SYNC_INTERVAL")
	if urlValue == "" && clusterID == "" && name == "" && intervalValue == "" {
		return ReporterConfig{}, false, nil
	}
	if urlValue == "" {
		return ReporterConfig{}, false, errors.New("Fleet sync configuration is incomplete")
	}
	config := ReporterConfig{URL: urlValue, ClusterID: clusterID, Name: name, Interval: defaultSyncInterval}
	if intervalValue != "" {
		interval, err := time.ParseDuration(intervalValue)
		if err != nil {
			return ReporterConfig{}, true, errors.New("Fleet sync interval is invalid")
		}
		config.Interval = interval
	}
	if err := validateReporterConfig(config); err != nil {
		return ReporterConfig{}, true, err
	}
	return config, true, nil
}

func NewReporter(config ReporterConfig, capture CaptureFunc) (*Reporter, error) {
	if config.Name == "" {
		config.Name = config.ClusterID
	}
	if err := validateReporterConfig(config); err != nil {
		return nil, err
	}
	if capture == nil {
		return nil, errors.New("Fleet sync capture function is required")
	}
	return &Reporter{
		config:  config,
		capture: capture,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func validateReporterConfig(config ReporterConfig) error {
	if !ValidateID(config.ClusterID) || config.Interval < minimumSyncInterval {
		return errors.New("Fleet sync cluster ID or interval is invalid")
	}
	if strings.TrimSpace(config.Name) != "" && len(config.Name) > 256 {
		return errors.New("Fleet sync cluster name is invalid")
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" ||
		strings.Contains(config.URL, "#") || parsed.Opaque != "" {
		return errors.New("Fleet sync URL must be an HTTP or HTTPS URL without credentials, query, or fragment")
	}
	return nil
}

func (r *Reporter) Run(ctx context.Context, onError func(error)) {
	for ctx.Err() == nil {
		attemptCtx, cancel := context.WithTimeout(ctx, captureTimeout)
		snapshot, err := r.capture(attemptCtx)
		if err == nil && ctx.Err() == nil {
			err = r.postSnapshot(attemptCtx, snapshot)
		}
		cancel()
		if err != nil && ctx.Err() == nil && onError != nil {
			onError(errors.New("Fleet sync attempt failed"))
		}
		timer := time.NewTimer(r.config.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (r *Reporter) postSnapshot(ctx context.Context, snapshot Snapshot) error {
	if snapshot.ClusterID != r.config.ClusterID || snapshot.Name != r.config.Name {
		return errors.New("snapshot identity does not match Fleet sync configuration")
	}
	if err := snapshot.Validate(time.Now()); err != nil {
		return errors.New("captured snapshot is invalid")
	}
	data, err := json.Marshal(snapshot)
	if err != nil || len(data) > MaxSnapshotBytes {
		return errors.New("snapshot exceeds the size limit")
	}
	endpoint := strings.TrimRight(r.config.URL, "/") + "/api/snapshots"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return errors.New("Fleet sync request could not be created")
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return errors.New("Fleet sync request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil || len(body) > responseLimit {
		return errors.New("Fleet sync response exceeds the size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return errors.New("Fleet sync endpoint rejected the snapshot")
	}
	var acknowledgment struct {
		OK bool `json:"ok"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&acknowledgment) != nil || !acknowledgment.OK || decoder.Decode(new(any)) != io.EOF {
		return errors.New("Fleet sync endpoint returned an invalid response")
	}
	return nil
}
