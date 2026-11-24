package alertpush

import (
	"context"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
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
		out = append(out, imageCandidate{role: ImageRoleReference, relPath: ev.Face.GalleryImagePath})
	}
	return out
}

// resolveImageURLs implements the url image mode: every candidate that can be signed or
// presigned becomes an ImageInfo; the rest are omitted.
func resolveImageURLs(ctx context.Context, p imageURLParams, ev record.AlarmEvent) []ImageInfo {
	images := []ImageInfo{}
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
		info := newImageInfo(c.role)
		info.URL = &signed.url
		info.URLExpiresAt = &signed.expiresAt
		info.SizeBytes = signed.sizeBytes
		images = append(images, info)
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

	if presigner, ok := p.Media.(MediaPresigner); ok {
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
		out.sizeBytes = statImageSize(ctx, p.Media, relPath)
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

func newImageInfo(role string) ImageInfo {
	return ImageInfo{Role: role, MIME: imageMIMEJPEG}
}
