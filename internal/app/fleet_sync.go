package app

import (
	"context"
	"errors"
	"log"

	"github.com/maomada/KubeAtlas/internal/cloud"
	"github.com/maomada/KubeAtlas/internal/fleetsync"
	"github.com/maomada/KubeAtlas/internal/k8s"
	mcppkg "github.com/maomada/KubeAtlas/internal/mcp"
)

func StartFleetSync(ctx context.Context, cfg AppConfig) {
	startFleetSync(ctx, cfg, cfg.CloudTunnelConfigured || cloud.Mode(),
		fleetsync.ConfigFromEnv,
		k8s.ClusterSafetyBinding,
		k8s.GetContextName,
		func() string { return k8s.GetConnectionStatus().ClusterName },
		mcppkg.CaptureFleetSnapshot,
		func(ctx context.Context, config fleetsync.ReporterConfig, capture fleetsync.CaptureFunc, binding string) error {
			reporter, err := fleetsync.NewReporter(config, capture)
			if err != nil {
				return err
			}
			var agent *fleetsync.AgentClient
			var executor *mcppkg.FleetToolExecutor
			if cfg.MCPEnabled {
				executor, err = mcppkg.NewFleetToolExecutor(ctx, binding, !cfg.MCPReadOnly,
					cfg.MCPExecTools && !cfg.MCPReadOnly && !cfg.DisableExec)
				if err != nil {
					return err
				}
				agent, err = fleetsync.NewAgentClient(config, fleetsync.SourceIDForBinding(binding), executor)
				if err != nil {
					executor.Close()
					return err
				}
			}
			log.Printf("[fleet-sync] authenticated Portal snapshot synchronization enabled")
			go reporter.Run(ctx, func(error) {
				log.Printf("[fleet-sync] snapshot attempt failed")
			})
			if agent != nil {
				log.Printf("[fleet-sync] outbound Portal MCP channel enabled")
				agentCtx := executor.LifetimeContext()
				go agent.Run(agentCtx, func(error) {
					log.Printf("[fleet-sync] MCP channel unavailable")
				})
				go func() {
					<-agentCtx.Done()
					executor.Close()
				}()
			}
			return nil
		})
}

type fleetCaptureFunc func(context.Context, string, string, string) (fleetsync.Snapshot, error)

func startFleetSync(
	ctx context.Context,
	cfg AppConfig,
	cloudMode bool,
	loadConfig func() (fleetsync.ReporterConfig, bool, error),
	clusterBinding func(context.Context) string,
	currentContext func() string,
	clusterName func() string,
	captureSnapshot fleetCaptureFunc,
	startReporter func(context.Context, fleetsync.ReporterConfig, fleetsync.CaptureFunc, string) error,
) bool {
	if cloudMode {
		log.Printf("[fleet-sync] synchronization disabled in Cloud mode")
		return false
	}
	config, enabled, err := loadConfig()
	if err != nil {
		log.Printf("[fleet-sync] synchronization disabled: invalid configuration")
		return false
	}
	if !enabled {
		return false
	}
	binding := clusterBinding(ctx)
	if binding == "" {
		log.Printf("[fleet-sync] synchronization disabled: cluster connection identity is unavailable")
		return false
	}
	startupContext := currentContext()
	if config.Name == "" {
		config.Name = clusterName()
		if config.Name == "" {
			config.Name = config.ClusterID
		}
	}
	capture := pinnedFleetCapture(config, binding, startupContext, currentContext, captureSnapshot)
	if err := startReporter(ctx, config, capture, binding); err != nil {
		log.Printf("[fleet-sync] synchronization disabled: invalid configuration")
		return false
	}
	return true
}

func pinnedFleetCapture(
	config fleetsync.ReporterConfig,
	binding string,
	startupContext string,
	currentContext func() string,
	captureSnapshot fleetCaptureFunc,
) fleetsync.CaptureFunc {
	return func(ctx context.Context) (fleetsync.Snapshot, error) {
		if currentContext() != startupContext {
			return fleetsync.Snapshot{}, errors.New("Fleet sync startup context changed")
		}
		snapshot, err := captureSnapshot(ctx, config.ClusterID, config.Name, binding)
		if err != nil {
			return fleetsync.Snapshot{}, err
		}
		if currentContext() != startupContext {
			return fleetsync.Snapshot{}, errors.New("Fleet sync startup context changed")
		}
		return snapshot, nil
	}
}
