package main

import (
	"log/slog"

	"github.com/lanscarlos/hypercraft/internal/confighist"
	"github.com/lanscarlos/hypercraft/internal/instance"
	"github.com/lanscarlos/hypercraft/internal/plugin"
)

// wireConfigHistory hangs the config history off the instance lifecycle.
//
// The snapshots have to be taken here rather than in the HTTP handlers,
// because two of the starts that matter most never pass through one: an
// instance flagged AutoStart comes up when the machine boots, and a crashed
// one comes back by itself. Those are the runs nobody was watching, and so
// the ones whose before/after diff is worth the most.
func wireConfigHistory(
	history *confighist.Service,
	manager *instance.Manager,
	plugins *plugin.Instances,
	logger *slog.Logger,
) {
	history.SetManifest(instanceManifest(manager, plugins))
	if plugins != nil {
		plugins.SetConfigHistory(history)
	}

	snapshot := func(cfg instance.Config, message string) {
		if _, shared := manager.DirectoryConflict(cfg.ID); shared {
			return
		}
		result, err := history.Commit(confighist.CommitRequest{
			InstanceID: cfg.ID,
			Directory:  cfg.Directory,
			Message:    message,
			Trigger:    confighist.TriggerLifecycle,
			Actor:      confighist.ActorLifecycle,
		})
		switch {
		case err == nil:
			if !result.Skipped {
				logger.Info("配置历史已记录", "instance", cfg.Name, "message", message,
					"files", result.Stats.Files)
			}
		case err == confighist.ErrDisabled:
			// Off for this instance; nothing to say every time it starts.
		default:
			// Loud, and only a log line. A gate that stopped the snapshot is
			// something the operator has to act on — the tab shows it — but a
			// server must never fail to start because its history could not be
			// written.
			logger.Warn("配置历史没能记录", "instance", cfg.Name, "message", message, "err", err)
		}
	}

	manager.SetHooks(instance.Hooks{
		BeforeStart: func(cfg instance.Config) { snapshot(cfg, "启动前快照") },
		AfterStop:   func(cfg instance.Config) { snapshot(cfg, "停止后快照") },
	}, func(cfg instance.Config) {
		// A new instance sharing a directory with an existing one gets the
		// module switched off rather than a history it cannot make sense of.
		// See the design's §10.
		if other, shared := manager.DirectoryConflict(cfg.ID); shared {
			logger.Warn("实例与另一台共用目录，已停用配置历史",
				"instance", cfg.Name, "other", other)
			if err := history.SetEnabled(cfg.ID, false); err != nil {
				logger.Warn("停用配置历史失败", "instance", cfg.Name, "err", err)
			}
			return
		}
		snapshot(cfg, "出厂状态")
	})
}

// instanceManifest reports what a server is running, so a restore can warn
// when the configuration being put back predates the jars it will run under.
// See the design's §5.3.
func instanceManifest(manager *instance.Manager, plugins *plugin.Instances) confighist.ManifestFunc {
	return func(instanceID string) confighist.Manifest {
		inst, err := manager.Get(instanceID)
		if err != nil {
			return confighist.Manifest{}
		}
		cfg := inst.Config()

		manifest := confighist.Manifest{Core: cfg.Jar}
		if plugins == nil {
			return manifest
		}
		for _, entry := range plugins.Records(instanceID) {
			name := entry.PluginName
			if name == "" {
				name = entry.PluginID
			}
			manifest.Plugins = append(manifest.Plugins, name+" "+entry.Version)
		}
		return manifest
	}
}
