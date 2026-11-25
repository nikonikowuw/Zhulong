package alertpush

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

const (
	imageMIMEPNG  = "image/png"
	imageMIMEWEBP = "image/webp"
)

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
	if isFace && ev.Face != nil && ev.Face.GalleryImagePath != "" {
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
// signed host URL. withSize opens the local file only to read its size.
func mintImageURL(
	ctx context.Context,
	p imageURLParams,
	relPath string,
	withSize bool,
) (signedImageURL, bool) {
	expiresAt := formatPayloadTime(time.Now().Add(p.TTL))
	reader := mediaReaderForPath(p.Media, p.GalleryMedia, relPath)

	if presigner, ok := reader.(MediaPresigner); ok {
		urlStr, handled, err := presigner.PresignMediaURL(ctx, relPath, p.TTL)
		if err == nil && handled && urlStr != "" {
			return signedImageURL{url: urlStr, expiresAt: expiresAt}, true
		}
	}

	if p.Signer == nil || strings.TrimSpace(p.PublicBaseURL) == "" {
		return signedImageURL{}, false
	}
	urlStr, err := generateSignedURL(
		p.PublicBaseURL,
		p.Signer,
		relPath,
		p.TTL,
	)
	if err != nil || urlStr == "" {
		return signedImageURL{}, false
	}

	out := signedImageURL{url: urlStr, expiresAt: expiresAt}
	if withSize {
		out.sizeBytes = statImageSize(ctx, reader, relPath)
	}
	return out, true
}

// statImageSize reads an image's byte size through the media reader and closes it at once.
func statImageSize(ctx context.Context, media MediaReader, relPath string) *int64 {
	if media == nil {
		return nil
	}
	f, info, err := media.OpenContent(ctx, relPath)
	if err != nil {
		return nil
	}
	if closeErr := f.Close(); closeErr != nil {
		return nil
	}
	size := info.SizeBytes
	return &size
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
