package algorithm

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	// The verifier decodes whatever common format a package ships. Register the
	// standard-library decoders the convention files allow (sample.jpg/png).
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
)

const (
	// MaxTrialImageDimension bounds width and height of an uploaded trial image.
	MaxTrialImageDimension = 8192
	// MaxTrialUploadSize bounds trial image upload size to 10 MB.
	MaxTrialUploadSize int64 = 10 * 1024 * 1024
)

var (
	ErrInvalidImageFile        = errors.New("invalid or unsupported image file, must be JPEG or PNG")
	ErrImageDimensionsTooLarge = errors.New("image dimensions exceed 8192px limit")
	ErrTrialTimeout            = errors.New("trial inference execution timed out")
	ErrTrialExecutionFailed    = errors.New("trial inference execution failed")
)

// writeSampleFramePPM decodes the package sample image into a binary PPM (P6) file
// the package entry can read without linking any image decoder itself, and returns
// its path plus a cleanup func.
//
// The package skeleton deliberately has no JPEG/PNG dependency: image decoding is
// not part of the inference path. Handing it a decoded frame keeps
// `<entry> --verify --sample` an honest black-box check (the packaged sample really
// goes through the model) instead of a lifecycle-only smoke test on a synthetic
// frame.
//
// Returns ("", nil) when the image cannot be decoded; callers then fall back to
// passing the raw path, and the package reports sample_decoded=false so the
// fallback stays visible rather than silently "passing".
func writeSampleFramePPM(samplePath string) (string, func()) {
	file, err := os.Open(samplePath)
	if err != nil {
		return "", nil
	}
	defer file.Close()

	decoded, _, err := image.Decode(file)
	if err != nil {
		return "", nil
	}

	ppmPath, cleanup, err := writeImageToPPM(decoded, "zhulong_sample_*.ppm")
	if err != nil {
		return "", nil
	}
	return ppmPath, cleanup
}

// writeImageToPPM writes an image.Image into a temporary binary PPM (P6) file,
// returning the file path and a cleanup function to remove it.
func writeImageToPPM(img image.Image, prefix string) (string, func(), error) {
	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return "", nil, errors.New("non-positive image dimensions")
	}

	temp, err := os.CreateTemp("", prefix)
	if err != nil {
		return "", nil, fmt.Errorf("create temp ppm: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(temp.Name())
	}

	writer := bufio.NewWriterSize(temp, 1<<20)
	if _, err := fmt.Fprintf(writer, "P6\n%d %d\n255\n", width, height); err != nil {
		_ = temp.Close()
		cleanup()
		return "", nil, fmt.Errorf("write ppm header: %w", err)
	}

	// Rows are converted by hand instead of using draw.Draw: the runner wants packed
	// RGB24, and a per-row conversion keeps the sample well inside a single
	// buffered writer pass without an intermediate image.RGBA copy.
	row := make([]byte, width*3)

	switch concrete := img.(type) {
	case *image.RGBA:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			rowOffset := 0
			pixOffset := concrete.PixOffset(bounds.Min.X, y)
			for x := 0; x < width; x++ {
				i := pixOffset + x*4
				row[rowOffset] = concrete.Pix[i]
				row[rowOffset+1] = concrete.Pix[i+1]
				row[rowOffset+2] = concrete.Pix[i+2]
				rowOffset += 3
			}
			if _, err := writer.Write(row); err != nil {
				_ = temp.Close()
				cleanup()
				return "", nil, fmt.Errorf("write ppm row: %w", err)
			}
		}
	case *image.NRGBA:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			rowOffset := 0
			pixOffset := concrete.PixOffset(bounds.Min.X, y)
			for x := 0; x < width; x++ {
				i := pixOffset + x*4
				row[rowOffset] = concrete.Pix[i]
				row[rowOffset+1] = concrete.Pix[i+1]
				row[rowOffset+2] = concrete.Pix[i+2]
				rowOffset += 3
			}
			if _, err := writer.Write(row); err != nil {
				_ = temp.Close()
				cleanup()
				return "", nil, fmt.Errorf("write ppm row: %w", err)
			}
		}
	case *image.YCbCr:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			rowOffset := 0
			yi := concrete.YOffset(bounds.Min.X, y)
			for x := 0; x < width; x++ {
				ci := concrete.COffset(bounds.Min.X+x, y)
				r, g, b := color.YCbCrToRGB(concrete.Y[yi+x], concrete.Cb[ci], concrete.Cr[ci])
				row[rowOffset] = r
				row[rowOffset+1] = g
				row[rowOffset+2] = b
				rowOffset += 3
			}
			if _, err := writer.Write(row); err != nil {
				_ = temp.Close()
				cleanup()
				return "", nil, fmt.Errorf("write ppm row: %w", err)
			}
		}
	default:
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			rowOffset := 0
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				row[rowOffset] = byte(r >> 8)
				row[rowOffset+1] = byte(g >> 8)
				row[rowOffset+2] = byte(b >> 8)
				rowOffset += 3
			}
			if _, err := writer.Write(row); err != nil {
				_ = temp.Close()
				cleanup()
				return "", nil, fmt.Errorf("write ppm row: %w", err)
			}
		}
	}
	if err := writer.Flush(); err != nil {
		_ = temp.Close()
		cleanup()
		return "", nil, fmt.Errorf("flush ppm: %w", err)
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close ppm: %w", err)
	}

	return temp.Name(), cleanup, nil
}

// trialImagePPM wraps the temporary PPM file and its dimensions for trial inference.
type trialImagePPM struct {
	Path    string
	Width   int
	Height  int
	Cleanup func()
}

// Close removes the temporary PPM file.
func (t *trialImagePPM) Close() {
	if t != nil && t.Cleanup != nil {
		t.Cleanup()
	}
}

// writeTrialImagePPM decodes an incoming JPEG or PNG image stream, verifies
// dimensions, and serializes it to a temporary PPM P6 file for trial inference.
func writeTrialImagePPM(r io.Reader) (*trialImagePPM, error) {
	var data []byte
	if bp, ok := r.(interface{ Bytes() []byte }); ok {
		data = bp.Bytes()
	} else {
		var err error
		data, err = io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to read image data: %v", ErrInvalidImageFile, err)
		}
	}

	// 1. Inspect image config first to reject huge images before allocating full pixel buffers.
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImageFile, err)
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, fmt.Errorf("%w: non-positive dimensions", ErrInvalidImageFile)
	}
	if config.Width > MaxTrialImageDimension || config.Height > MaxTrialImageDimension {
		return nil, fmt.Errorf(
			"%w: %dx%d exceeds %d limit",
			ErrImageDimensionsTooLarge,
			config.Width,
			config.Height,
			MaxTrialImageDimension,
		)
	}

	// 2. Decode full image and perform secondary verification.
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidImageFile, err)
	}

	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > MaxTrialImageDimension || height > MaxTrialImageDimension {
		return nil, fmt.Errorf(
			"%w: %dx%d exceeds %d limit",
			ErrImageDimensionsTooLarge,
			width,
			height,
			MaxTrialImageDimension,
		)
	}

	ppmPath, cleanup, err := writeImageToPPM(decoded, "zhulong_trial_*.ppm")
	if err != nil {
		return nil, err
	}

	return &trialImagePPM{
		Path:    ppmPath,
		Width:   width,
		Height:  height,
		Cleanup: cleanup,
	}, nil
}
