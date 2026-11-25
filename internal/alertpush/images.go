package alertpush

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

const (
	imageMIMEPNG        = "image/png"
	imageMIMEWEBP       = "image/webp"
	defaultSignedURLTTL = 1 * time.Hour
)

// MediaStatter optionally exposes metadata lookup without opening a full byte stream
// (e.g. mediafile.Service.GetEntry uses Stat / S3 HEAD instead of GetObject).
type MediaStatter interface {
	GetEntry(ctx context.Context, relPath string) (*mediafile.MediaEntry, error)
}

// imageCandidate is one evidence image an event may carry, before any carrier is chosen.
type imageCandidate struct {
	role    string
	relPath string
}

// imageURLParams bundles what is needed to mint an image URL for one event.
type imageURLParams struct {
	PublicBaseURL string
	Signer        *URLSigner
	Media         MediaReader
	GalleryMedia  MediaReader
	TTL           time.Duration
}

// signedImageURL is a minted image URL. sizeBytes is only known when the local file was
// opened; presigned object-store URLs leave it nil to avoid fetching the remote object.
type signedImageURL struct {
	url       string
	expiresAt string
	sizeBytes *int64
}

// imageCandidates lists the event's ready images in the fixed output order
// scene → object → reference.
func imageCandidates(ev record.AlarmEvent) []imageCandidate {
	out := []imageCandidate{}
	if ev.ImageReady && ev.ImagePath != "" {
		out = append(out, imageCandidate{role: ImageRoleScene, relPath: ev.ImagePath})
	}
	if ev.CropReady && ev.CropPath != "" {
		out = append(out, imageCandidate{role: ImageRoleObject, relPath: ev.CropPath})
	}
	isFace := ev.EventType == record.EventTypeFaceRecognition
	if isFace && ev.Face != nil && ev.Face.Matched && ev.Face.GalleryImagePath != "" {
		if normRef, ok := normalizeGalleryRelPath(ev.Face.GalleryImagePath); ok {
			out = append(out, imageCandidate{role: ImageRoleReference, relPath: normRef})
		}
	}
	return out
}

// normalizeGalleryRelPath converts person_faces.image_path (bare filename in production,
// or faces/<file> / snapshots/<...> in legacy fixtures) into a validated logical key.
func normalizeGalleryRelPath(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	norm := normalizeRelativePath(trimmed)
	if norm == "" {
		return "", false
	}
	if isAllowedSnapshotSignedPath(norm) {
		return norm, true
	}
	if isAllowedFaceSignedPath(norm) {
		return norm, true
	}
	if !strings.Contains(norm, "/") {
		candidate := filestore.NamespaceFaces + "/" + norm
		if isAllowedFaceSignedPath(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// mediaReaderForPath routes faces/ keys to the dedicated gallery reader when configured,
// keeping /media/content/* isolated from enrolled face photos while letting unit tests
// stub a single MediaReader.
func mediaReaderForPath(media, galleryMedia MediaReader, relPath string) MediaReader {
	if isAllowedFaceSignedPath(normalizeRelativePath(relPath)) && galleryMedia != nil {
		return galleryMedia
	}
	return media
}

// resolveImageURLs implements the url image mode: every candidate that can be signed or
// presigned populates its role on EventImages; returns nil when none are deliverable.
func resolveImageURLs(ctx context.Context, p imageURLParams, ev record.AlarmEvent) *EventImages {
	images := &EventImages{}
	for _, c := range imageCandidates(ev) {
		signed, ok := mintImageURL(
			ctx,
			p,
			c.relPath,
			true,
		)
		if !ok {
			continue
		}
		info := newImageInfo(c.relPath)
		info.URL = signed.url
		info.ExpiresAt = signed.expiresAt
		info.SizeBytes = signed.sizeBytes
		images.set(c.role, info)
	}
	if images.Empty() {
		return nil
	}
	return images
}

// mintImageURL resolves a presigned object-store URL first and falls back to an HMAC
// signed host URL. withSize inspects metadata (via MediaStatter when available) and
// omits minting a broken URL if the media object is confirmed missing.
func mintImageURL(
	ctx context.Context,
	p imageURLParams,
	relPath string,
	withSize bool,
) (signedImageURL, bool) {
	ttl := p.TTL
	if ttl <= 0 {
		ttl = defaultSignedURLTTL
	}
	expTime := time.Now().UTC().Add(ttl).Truncate(time.Second)
	expiresAt := formatPayloadTime(expTime)
	exp := expTime.Unix()
	reader := mediaReaderForPath(p.Media, p.GalleryMedia, relPath)

	if presigner, ok := reader.(MediaPresigner); ok {
		urlStr, handled, err := presigner.PresignMediaURL(ctx, relPath, ttl)
		if err == nil && handled && urlStr != "" {
			return signedImageURL{url: urlStr, expiresAt: expiresAt}, true
		}
	}

	if p.Signer == nil || strings.TrimSpace(p.PublicBaseURL) == "" {
		return signedImageURL{}, false
	}

	var sizeBytes *int64
	if withSize && reader != nil {
		size, exists := statImageSize(ctx, reader, relPath)
		if !exists {
			return signedImageURL{}, false
		}
		sizeBytes = size
	}

	urlStr, err := generateSignedURLAt(
		p.PublicBaseURL,
		p.Signer,
		relPath,
		exp,
	)
	if err != nil || urlStr == "" {
		return signedImageURL{}, false
	}

	return signedImageURL{
		url:       urlStr,
		expiresAt: expiresAt,
		sizeBytes: sizeBytes,
	}, true
}

func isMediaMissingErr(err error) bool {
	if err == nil {
		return false
	}
	if filestore.IsNotFound(err) ||
		errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, filestore.ErrNotAFile) {
		return true
	}
	var appErr *apperr.Error
	return errors.As(err, &appErr) && appErr.Kind == apperr.KindNotFound
}

// statImageSize resolves an image's byte size without downloading object bodies whenever
// the reader implements MediaStatter (e.g. mediafile.Service.GetEntry). Returns exists=false
// only when the backing object is confirmed absent so callers avoid minting 404 URLs.
func statImageSize(ctx context.Context, media MediaReader, relPath string) (*int64, bool) {
	if media == nil {
		return nil, true
	}
	if statter, ok := media.(MediaStatter); ok {
		entry, err := statter.GetEntry(ctx, relPath)
		if err != nil {
			if isMediaMissingErr(err) {
				return nil, false
			}
			return nil, true
		}
		if entry != nil {
			size := entry.SizeBytes
			return &size, true
		}
		return nil, true
	}

	f, info, err := media.OpenContent(ctx, relPath)
	if err != nil {
		if isMediaMissingErr(err) {
			return nil, false
		}
		return nil, true
	}
	if closeErr := f.Close(); closeErr != nil {
		return nil, true
	}
	size := info.SizeBytes
	return &size, true
}

func newImageInfo(relPath string) *ImageInfo {
	return &ImageInfo{MIME: imageMIMEForPath(relPath)}
}

func imageMIMEForPath(relPath string) string {
	switch strings.ToLower(path.Ext(relPath)) {
	case ".png":
		return imageMIMEPNG
	case ".webp":
		return imageMIMEWEBP
	default:
		return imageMIMEJPEG
	}
}

func imageExtForMIME(mime string) string {
	switch mime {
	case imageMIMEPNG:
		return ".png"
	case imageMIMEWEBP:
		return ".webp"
	default:
		return ".jpg"
	}
}
