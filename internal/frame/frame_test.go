package frame

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestProcessScalesAndEncodes(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			source.Set(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 20), A: 255})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, source); err != nil {
		t.Fatal(err)
	}
	processed, err := Process(&raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(processed.RGB) != RGBByteSize || len(processed.PNG) == 0 || len(processed.SHA256) != 64 {
		t.Fatalf("processed = %+v", processed)
	}
}

func TestProcessRawRGB(t *testing.T) {
	raw := bytes.Repeat([]byte{1, 2, 3}, Width*Height)
	processed, err := Process(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(processed.RGB, raw) {
		t.Fatal("raw RGB changed")
	}
}
