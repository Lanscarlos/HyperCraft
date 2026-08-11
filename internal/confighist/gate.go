package confighist

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lanscarlos/hypercraft/internal/gitlite"
)

// GateKind names which of the design's §2 thresholds stopped a commit.
type GateKind string

const (
	GateFileSize  GateKind = "file-size"
	GateFileCount GateKind = "file-count"
	GateRepoSize  GateKind = "repo-size"
	// GateTruncated is not a threshold but the same class of failure: the scan
	// could not see the whole directory, so what it did see is not a snapshot.
	GateTruncated GateKind = "truncated"
)

// OversizedFile is one file over the per-file ceiling, with the size that put
// it there. The operator decides per file — record it anyway, or exclude it for
// good — so the list has to name them rather than count them.
type OversizedFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// GateError is a refused commit.
//
// Refusing loudly is the whole point. A gate that silently skipped the commit
// would leave an operator believing their configuration was recorded when it
// was not, which is worse than having no history at all — see the design's §2.
type GateError struct {
	Kind      GateKind        `json:"kind"`
	Limits    Limits          `json:"limits"`
	Oversized []OversizedFile `json:"oversized,omitempty"`
	Files     int             `json:"files,omitempty"`
	RepoBytes int64           `json:"repoBytes,omitempty"`
	Message   string          `json:"message"`
}

func (e *GateError) Error() string { return e.Message }

// AsGateError unwraps a refusal, so a caller can tell "the gates said no" from
// "the disk said no".
func AsGateError(err error) (*GateError, bool) {
	var gate *GateError
	if errors.As(err, &gate) {
		return gate, true
	}
	return nil, false
}

func (s *Service) checkGates(repo *gitlite.Repo, plan commitPlan, settings InstanceSettings) error {
	limits := settings.limits()

	if len(plan.oversized) > 0 {
		names := make([]string, 0, 3)
		for _, file := range plan.oversized {
			if len(names) == 3 {
				names = append(names, "…")
				break
			}
			names = append(names, fmt.Sprintf("%s（%s）", file.Path, humanBytes(file.Size)))
		}
		return &GateError{
			Kind:      GateFileSize,
			Limits:    limits,
			Oversized: plan.oversized,
			Message: fmt.Sprintf("%d 个文件超过单文件上限 %s，本次没有提交：%s。逐个确认收录或永久排除后再试",
				len(plan.oversized), humanBytes(limits.FileBytes), strings.Join(names, "、")),
		}
	}

	if len(plan.changed) > limits.FileCount {
		return &GateError{
			Kind:   GateFileCount,
			Limits: limits,
			Files:  len(plan.changed),
			Message: fmt.Sprintf("本次要提交 %d 个文件，超过单次上限 %d，已中止。这通常说明收录规则漏掉了某个数据目录，先看一眼再决定",
				len(plan.changed), limits.FileCount),
		}
	}

	size, err := repo.Size()
	if err != nil {
		return err
	}
	if size+plan.addedBytes > limits.RepoBytes {
		return &GateError{
			Kind:      GateRepoSize,
			Limits:    limits,
			RepoBytes: size,
			Message: fmt.Sprintf("配置历史仓库已有 %s，加上本次的 %s 会超过上限 %s，已中止。压缩历史或检查收录规则",
				humanBytes(size), humanBytes(plan.addedBytes), humanBytes(limits.RepoBytes)),
		}
	}
	return nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
