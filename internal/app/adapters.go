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

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"github.com/nikonikowuw/Zhulong/internal/task"
)

type appBrokerPathResolver struct {
	taskStore *task.Store
	streamHub *camera.StreamHub
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
		return nil, fmt.Errorf("list tasks for algorithm %s: %w", algorithmID, err)
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
				if _, err := task.ValidateSampleFPS(algo.SampleFPS); err != nil {
					return nil, fmt.Errorf("task %s algo %s: %w", tsk.ID, algo.AlgorithmID, err)
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
			return nil, fmt.Errorf("resolve broker path for camera %s: %w", cameraID, err)
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
			return nil, fmt.Errorf("set AI publish rate for camera %s: %w", cameraID, err)
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
				return nil, fmt.Errorf("task %s algo %s: %w", tsk.ID, algo.AlgorithmID, err)
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

// faceFeatureExtractorAdapter bridges *engine.Engine to person.FaceFeatureExtractor.
//
// The translation lives here rather than in internal/person so the domain package
// keeps its "no dependency on internal/engine" boundary. It also maps the engine's
// "no face found" status onto person.ErrNoFaceDetected (→ 422), and a decode failure
// onto person.ErrUnsupportedImageFormat (→ 400): an unusable upload must not be
// reported as a server error, and a genuine engine failure must not be blamed on it.
type faceFeatureExtractorAdapter struct {
	eng *engine.Engine
}

func (a *faceFeatureExtractorAdapter) ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error) {
	features, err := a.eng.ExtractFaceFeature(ctx, imageBytes)
	if err != nil {
		if errors.Is(err, engine.ErrNotFound) {
			return nil, fmt.Errorf("%w: %v", person.ErrNoFaceDetected, err)
		}
		if errors.Is(err, engine.ErrUnsupportedImageFormat) {
			return nil, fmt.Errorf("%w: %v", person.ErrUnsupportedImageFormat, err)
		}
		return nil, err
	}
	return features, nil
}
