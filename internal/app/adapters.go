package app

// Cross-domain adapters.
//
// Every adapter in this file exists to keep two domain packages from importing
// each other: one side owns the data, the other owns the interface it is asked
// to satisfy, and the translation lives in the assembly layer. None of them
// holds state beyond the single collaborator it was built with, which is what
// lets the same value be passed to a constructor in a foreign package.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/faceextract"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"github.com/nikonikowuw/Zhulong/internal/schedule"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
)

type appBrokerPathResolver struct {
	taskStore *task.Store
	streamHub *camera.StreamHub
	logger    *zap.Logger
}

// log returns the resolver's logger, tolerating the nil logger used by tests that
// only exercise the pure filtering paths.
func (r *appBrokerPathResolver) log() *zap.Logger {
	if r.logger == nil {
		return zap.NewNop()
	}
	return r.logger
}

// TaskConfigsFor 解析某个算法包在每一路流上的任务级配置（stream_id → config_json）。
//
// 与 SyncBrokerSubscriptions 的分工：那边决定「订阅哪些流」，这边只回答「每路流的算法
// 配置是什么」。两者都按 enabled + 排程过滤，否则会给一个休眠中的流推配置：那条流的
// broker 已经不存在，推送只会换来一条无意义的 NOT_FOUND 告警。
//
// ⚠️ 同一个相机 + 同一个算法在库中只应有一条 enabled 任务（子表 UNIQUE(task_id, algorithm_id)，
//
//	但不同 task 仍可指向同一相机）。真的出现两份时取任务顺序里的第一条并告警：静默挑一个
//	会让「改了配置没生效」变成无解问题。
func (r *appBrokerPathResolver) TaskConfigsFor(ctx context.Context, algorithmID string) (map[uint64]string, error) {
	if r.taskStore == nil || r.streamHub == nil {
		return nil, nil
	}
	tasks, err := r.taskStore.ListTasksByAlgorithm(ctx, algorithmID)
	if err != nil {
		return nil, fmt.Errorf("list tasks for algorithm %q: %w", algorithmID, err)
	}

	configs := make(map[uint64]string, len(tasks))
	owner := make(map[uint64]string, len(tasks))
	for _, tsk := range tasks {
		if !tsk.Enabled {
			continue
		}
		for _, algo := range tsk.Algorithms {
			if algo.AlgorithmID != algorithmID || !algo.Enabled {
				continue
			}
			sched, err := schedule.Parse(algo.ScheduleJSON)
			if err == nil && sched != nil && !sched.IsActiveAt(time.Now()) {
				continue
			}

			streamID, err := r.streamHub.ResolveAIStreamID(ctx, tsk.CameraID)
			if err != nil {
				return nil, fmt.Errorf("resolve stream id for camera %q: %w", tsk.CameraID, err)
			}

			cfg := algo.ConfigJSON
			if cfg == "" {
				cfg = "{}"
			}
			if prev, ok := configs[streamID]; ok {
				if prev != cfg {
					r.log().Warn("duplicate task config for the same stream and algorithm; keeping the first",
						zap.String("algorithm_id", algorithmID),
						zap.Uint64("stream_id", streamID),
						zap.String("kept_task_id", owner[streamID]),
						zap.String("dropped_task_id", tsk.ID),
					)
				}
				continue
			}
			configs[streamID] = cfg
			owner[streamID] = tsk.ID
		}
	}
	return configs, nil
}

// SyncBrokerSubscriptions 解析某个算法包当前应当订阅的全部 FrameBroker socket 路径，
// 并把该包的订阅关系同步到它在 DB 中的最新任务集合。
//
// 它同时承担三件事，且三者必须在同一次同步里完成，否则订阅关系会与 DB 中的任务集合漂移：
//  1. 按该包 enabled 的任务解析出每一路相机的 AI 流路径（失败即整体失败）；
//  2. 提交订阅关系：新出现的相机 attach，已不在任务集合里的相机 release；
//  3. 按「该相机上所有 enabled 任务的 max(sample_fps)」下发发布节流率。
//
// ⚠️ 解析失败时不提交任何订阅变更，也不会遗留已 acquire 的流：
// 所有路径解析完毕后才进入提交阶段。
func (r *appBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	if r.taskStore == nil || r.streamHub == nil {
		return nil, nil
	}
	tasks, err := r.taskStore.ListTasksByAlgorithm(ctx, algorithmID)
	if err != nil {
		return nil, fmt.Errorf("list tasks for algorithm %q: %w", algorithmID, err)
	}

	// 该包要求订阅的相机及其本包内的最大采样率；order 保持 DB 的稳定顺序。
	wanted := make(map[string]struct{}, len(tasks))
	order := make([]string, 0, len(tasks))
	for _, tsk := range tasks {
		if !tsk.Enabled {
			continue
		}
		for _, algo := range tsk.Algorithms {
			if algo.AlgorithmID == algorithmID && algo.Enabled {
				sched, err := schedule.Parse(algo.ScheduleJSON)
				if err == nil && sched != nil && !sched.IsActiveAt(time.Now()) {
					continue
				}
				if _, err := task.ValidateSampleFPS(algo.SampleFPS); err != nil {
					return nil, fmt.Errorf("task %q algo %q: %w", tsk.ID, algo.AlgorithmID, err)
				}
				if _, ok := wanted[tsk.CameraID]; !ok {
					wanted[tsk.CameraID] = struct{}{}
					order = append(order, tsk.CameraID)
				}
			}
		}
	}

	// 阶段 1：解析全部路径。任一路失败则整体失败，不触碰订阅关系。
	paths := make([]string, 0, len(order))
	for _, cameraID := range order {
		path, err := r.streamHub.ResolveAIStreamBrokerPath(ctx, cameraID)
		if err != nil {
			return nil, fmt.Errorf("resolve broker path for camera %q: %w", cameraID, err)
		}
		paths = append(paths, path)
	}

	// 阶段 2：提交订阅关系。
	existing := r.streamHub.AICameraIDs()
	for _, cameraID := range order {
		r.streamHub.AttachAIStreamSubscriber(cameraID, algorithmID)
	}
	for _, cameraID := range existing {
		if _, ok := wanted[cameraID]; !ok {
			// 该相机已不在本包的任务集合中：注销订阅，最后一个订阅者离开时释放流与 broker。
			r.streamHub.ReleaseAIStreamSubscriber(cameraID, algorithmID)
		}
	}

	// 阶段 3：按全库 enabled 任务的 max(sample_fps) 下发节流率。
	// 一个 broker 只有一个发布率，因此必须跨算法包取 max（design.md §4.1）。
	rateByCamera, err := r.publishRateByCamera(ctx)
	if err != nil {
		return nil, err
	}
	for cameraID, fps := range rateByCamera {
		if err := r.streamHub.SetAIPublishRate(ctx, cameraID, fps); err != nil {
			return nil, fmt.Errorf("set AI publish rate for camera %q: %w", cameraID, err)
		}
	}

	return paths, nil
}

// publishRateByCamera 返回每个相机上所有 enabled 分析任务的最大采样帧率。
func (r *appBrokerPathResolver) publishRateByCamera(ctx context.Context) (map[string]uint32, error) {
	tasks, err := r.taskStore.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("list analysis tasks for publish rate: %w", err)
	}
	rates := make(map[string]uint32)
	for _, tsk := range tasks {
		if !tsk.Enabled {
			continue
		}
		for _, algo := range tsk.Algorithms {
			if !algo.Enabled {
				continue
			}
			fps, err := task.ValidateSampleFPS(algo.SampleFPS)
			if err != nil {
				return nil, fmt.Errorf("task %q algo %q: %w", tsk.ID, algo.AlgorithmID, err)
			}
			if fps > rates[tsk.CameraID] {
				rates[tsk.CameraID] = fps
			}
		}
	}
	return rates, nil
}

type personGalleryAdapter struct {
	broadcaster *algorunner.GalleryBroadcaster
}

func (a *personGalleryAdapter) BroadcastUpsert(ctx context.Context, item person.GalleryItem) error {
	if a.broadcaster == nil {
		return nil
	}
	return a.broadcaster.BroadcastUpsert(ctx, algorunner.GalleryUpsertPayload{
		FaceID:     item.FaceID,
		PersonID:   item.PersonID,
		PersonName: item.PersonName,
		ImagePath:  item.ImagePath,
		Features:   item.Features,
	})
}

func (a *personGalleryAdapter) BroadcastDelete(ctx context.Context, faceID, personID string) error {
	if a.broadcaster == nil {
		return nil
	}
	return a.broadcaster.BroadcastDelete(ctx, faceID, personID)
}

// personKeyBackfillerAdapter bridges the person domain's first-time business-key binding
// onto recognitions already captured for that person.
//
// The translation lives here rather than in either domain: person owns the binding, the
// record store owns the recognitions table, and neither may reach into the other.
type personKeyBackfillerAdapter struct {
	store *record.Store
}

func (a *personKeyBackfillerAdapter) BackfillPersonExternalID(ctx context.Context, personRowID, externalID string) (int64, error) {
	if a.store == nil {
		return 0, nil
	}
	return a.store.BackfillPersonExternalID(ctx, personRowID, externalID)
}

type recordSnapshotFileChecker struct {
	media *mediafile.Service
}

func (c recordSnapshotFileChecker) SnapshotFileExists(ctx context.Context, relativePath string) (bool, error) {
	if c.media == nil {
		return false, errors.New("media file service is unavailable")
	}
	_, err := c.media.GetEntry(ctx, relativePath)
	if err == nil {
		return true, nil
	}
	var domainErr *apperr.Error
	if errors.As(err, &domainErr) && domainErr.Kind == apperr.KindNotFound {
		return false, nil
	}
	return false, err
}

// recordRetentionDaysProvider 把 storage 域持有的抓拍保留天数暴露给 record 域。
//
// 存在的理由：record 只认得「证据图还能活几天」这一个整数，不应该依赖
// storage.StorageConfig 或 storage.Service 的任何其他能力（保留天数每次现取，
// 跟着 UpdateConfig 热生效，不得缓存）。
// 未配置（nil service / 配置读不到）时返回 0，即「不启用清理」：宁可不删，
// 也不能拿猜的天数去删证据。
type recordRetentionDaysProvider struct {
	storage *storage.Service
}

func (p recordRetentionDaysProvider) SnapshotRetentionDays(ctx context.Context) (int, error) {
	if p.storage == nil {
		return 0, nil
	}
	cfg, err := p.storage.GetConfig(ctx)
	if err != nil {
		return 0, err
	}
	if cfg == nil {
		return 0, nil
	}
	return cfg.SnapshotsRetentionDays, nil
}

type supervisorGallerySyncAdapter struct {
	service *person.Service
}

func (a *supervisorGallerySyncAdapter) GetGallerySyncData(ctx context.Context) ([]algorunner.GallerySyncItem, error) {
	if a.service == nil {
		return nil, nil
	}
	items, err := a.service.GetGallerySyncData(ctx)
	if err != nil {
		return nil, err
	}
	algoItems := make([]algorunner.GallerySyncItem, len(items))
	for i, item := range items {
		algoItems[i] = algorunner.GallerySyncItem{
			FaceID:     item.FaceID,
			PersonID:   item.PersonID,
			PersonName: item.PersonName,
			ImagePath:  item.ImagePath,
			Features:   item.Features,
		}
	}
	return algoItems, nil
}

// faceFeatureExtractorAdapter bridges internal/faceextract to person.FaceFeatureExtractor.
//
// The translation lives here rather than in internal/person so the domain package
// keeps its "no dependency on process/pipe/codec" boundary. The mapping rules:
//
//   - an undecodable upload, or one the size guard rejects → person.ErrUnsupportedImageFormat
//     (the caller's input is unusable; answering 500 would blame the server);
//   - "no face in the photo" → person.ErrNoFaceDetected;
//   - anything else (crash, timeout, protocol desync, package missing) → the raw error,
//     which the service reports as a server failure.
//
// It never fabricates a vector: a zero embedding is indistinguishable from a real
// enrollment in storage and would silently never match.
type faceFeatureExtractorAdapter struct {
	client *faceextract.Client
}

func (a *faceFeatureExtractorAdapter) ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error) {
	if len(imageBytes) == 0 {
		return nil, fmt.Errorf("%w: empty image", person.ErrUnsupportedImageFormat)
	}

	pixels, width, height, err := faceextract.DecodeToRGB24(imageBytes)
	if err != nil {
		// Decoding happens on the host: the package skeleton links no codec.
		return nil, fmt.Errorf("%w: %v", person.ErrUnsupportedImageFormat, err)
	}

	features, err := a.client.Extract(ctx, faceextract.Frame{
		Width:  uint32(width),
		Height: uint32(height),
		Format: faceextract.FormatRGB24,
		Pixels: pixels,
	})
	if err != nil {
		switch {
		case errors.Is(err, faceextract.ErrNoFaceDetected):
			return nil, fmt.Errorf("%w: %v", person.ErrNoFaceDetected, err)
		case errors.Is(err, faceextract.ErrInvalidArgument):
			return nil, fmt.Errorf("%w: %v", person.ErrUnsupportedImageFormat, err)
		}
		return nil, err
	}
	return features, nil
}
