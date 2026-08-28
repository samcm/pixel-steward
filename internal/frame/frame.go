package frame

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
)

const (
	Width       = 64
	Height      = 64
	RGBByteSize = Width * Height * 3
	MaxInput    = 32 << 20
)

type Processed struct {
	PNG    []byte
	RGB    []byte
	SHA256 string
	Width  int
	Height int
}

func Process(source io.Reader) (Processed, error) {
	data, err := io.ReadAll(io.LimitReader(source, MaxInput+1))
	if err != nil {
		return Processed{}, err
	}
	if len(data) > MaxInput {
		return Processed{}, fmt.Errorf("frame exceeds %d-byte input limit", MaxInput)
	}

	var sourceImage image.Image
	if len(data) == RGBByteSize {
		imageValue := image.NewRGBA(image.Rect(0, 0, Width, Height))
		for index := 0; index < Width*Height; index++ {
			offset := index * 3
			imageValue.SetRGBA(index%Width, index/Width, color.RGBA{R: data[offset], G: data[offset+1], B: data[offset+2], A: 255})
		}
		sourceImage = imageValue
	} else {
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return Processed{}, fmt.Errorf("decode frame: %w", err)
		}
		sourceImage = decoded
	}

	destination := image.NewRGBA(image.Rect(0, 0, Width, Height))
	sourceBounds := sourceImage.Bounds()
	if sourceBounds.Dx() <= 0 || sourceBounds.Dy() <= 0 {
		return Processed{}, fmt.Errorf("frame has empty dimensions")
	}
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			sourceX := sourceBounds.Min.X + x*sourceBounds.Dx()/Width
			sourceY := sourceBounds.Min.Y + y*sourceBounds.Dy()/Height
			destination.Set(x, y, sourceImage.At(sourceX, sourceY))
		}
	}

	rgb := make([]byte, 0, RGBByteSize)
	for y := 0; y < Height; y++ {
		for x := 0; x < Width; x++ {
			r, g, b, _ := destination.At(x, y).RGBA()
			rgb = append(rgb, byte(r>>8), byte(g>>8), byte(b>>8))
		}
	}

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, destination); err != nil {
		return Processed{}, err
	}
	digest := sha256.Sum256(rgb)

	return Processed{PNG: encoded.Bytes(), RGB: rgb, SHA256: hex.EncodeToString(digest[:]), Width: Width, Height: Height}, nil
}
