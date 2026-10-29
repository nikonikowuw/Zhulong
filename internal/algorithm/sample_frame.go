package algorithm

import (
	"bufio"
	"fmt"
	"image"
	// The verifier decodes whatever common format a package ships. Register the
	// standard-library decoders the convention files allow (sample.jpg/png).
	_ "image/jpeg"
	_ "image/png"
	"os"
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

	bounds := decoded.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return "", nil
	}

	temp, err := os.CreateTemp("", "zhulong_sample_*.ppm")
	if err != nil {
		return "", nil
	}
	cleanup := func() {
		_ = os.Remove(temp.Name())
	}

	writer := bufio.NewWriterSize(temp, 1<<20)
	if _, err := fmt.Fprintf(writer, "P6\n%d %d\n255\n", width, height); err != nil {
		_ = temp.Close()
		cleanup()
		return "", nil
	}

	// Rows are converted by hand instead of using draw.Draw: the runner wants packed
	// RGB24, and a per-row conversion keeps the 2.7MB sample well inside a single
	// buffered writer pass without an intermediate image.RGBA copy.
	row := make([]byte, 0, width*3)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row = row[:0]
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := decoded.At(x, y).RGBA()
			row = append(row, byte(r>>8), byte(g>>8), byte(b>>8))
		}
		if _, err := writer.Write(row); err != nil {
			_ = temp.Close()
			cleanup()
			return "", nil
		}
	}
	if err := writer.Flush(); err != nil {
		_ = temp.Close()
		cleanup()
		return "", nil
	}
	if err := temp.Close(); err != nil {
		cleanup()
		return "", nil
	}
	return temp.Name(), cleanup
}
