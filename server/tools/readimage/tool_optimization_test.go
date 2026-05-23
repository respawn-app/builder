package readimage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"builder/server/tools"
	"builder/shared/toolspec"
	"github.com/deepteams/webp"
	"github.com/deepteams/webp/animation"
)

func TestCall_OptimizesLargeJPEGToSmallerWebPOutput(t *testing.T) {
	workspace := t.TempDir()
	var original bytes.Buffer
	if err := jpeg.Encode(&original, generatedPhotoLikeImage(1024), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	if int64(original.Len()) < minOptimizationSizeBytes {
		t.Fatalf("test image is too small for optimization path: %d", original.Len())
	}
	if int64(original.Len()) <= maxFileSizeBytes {
		t.Fatalf("test image must exceed attachment cap before optimization: %d", original.Len())
	}
	imagePath := filepath.Join(workspace, "photo.jpg")
	if err := os.WriteFile(imagePath, original.Bytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-optimized",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"photo.jpg"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got error payload: %s", string(result.Output))
	}

	mimeType, payload := decodeSingleImageDataURL(t, result)
	if mimeType != "image/webp" {
		t.Fatalf("expected optimized webp output, got %q", mimeType)
	}
	if len(payload) >= original.Len() {
		t.Fatalf("expected optimized output smaller than original, got optimized=%d original=%d", len(payload), original.Len())
	}
	if int64(len(payload)) > maxFileSizeBytes {
		t.Fatalf("expected optimized output under attachment cap, got %d", len(payload))
	}
}

func TestCall_RawImageSkipsOptimization(t *testing.T) {
	workspace := t.TempDir()
	var original bytes.Buffer
	if err := jpeg.Encode(&original, generatedPhotoLikeImage(512), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	imagePath := filepath.Join(workspace, "photo.jpg")
	if err := os.WriteFile(imagePath, original.Bytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-raw",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"photo.jpg","raw":true}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success result, got error payload: %s", string(result.Output))
	}

	mimeType, payload := decodeSingleImageDataURL(t, result)
	if mimeType != "image/jpeg" {
		t.Fatalf("expected raw jpeg output, got %q", mimeType)
	}
	if !bytes.Equal(payload, original.Bytes()) {
		t.Fatalf("expected raw image bytes to be preserved")
	}
}

func TestCall_RawImageStillEnforcesAttachmentCap(t *testing.T) {
	workspace := t.TempDir()
	var original bytes.Buffer
	if err := jpeg.Encode(&original, generatedPhotoLikeImage(1024), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	if int64(original.Len()) <= maxFileSizeBytes {
		t.Fatalf("test image must exceed attachment cap: %d", original.Len())
	}
	imagePath := filepath.Join(workspace, "large.jpg")
	if err := os.WriteFile(imagePath, original.Bytes(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-raw-large",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"large.jpg","raw":true}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected raw oversized image to be rejected")
	}
	if got := toolError(t, result); !strings.Contains(got, "max supported size is 819200 bytes (800 KiB)") {
		t.Fatalf("expected attachment cap error, got %q", got)
	}
}

func TestCall_StillGIFAcceptedAndAnimatedGIFRejected(t *testing.T) {
	workspace := t.TempDir()
	stillPath := filepath.Join(workspace, "still.gif")
	if err := os.WriteFile(stillPath, encodedGIF(t, 1), 0o644); err != nil {
		t.Fatalf("write still gif: %v", err)
	}
	animatedPath := filepath.Join(workspace, "animated.gif")
	if err := os.WriteFile(animatedPath, encodedGIF(t, 2), 0o644); err != nil {
		t.Fatalf("write animated gif: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	still, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-still-gif",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"still.gif"}`),
	})
	if err != nil {
		t.Fatalf("still gif call: %v", err)
	}
	if still.IsError {
		t.Fatalf("expected still gif success, got %s", string(still.Output))
	}
	mimeType, _ := decodeSingleImageDataURL(t, still)
	if mimeType != "image/gif" {
		t.Fatalf("expected still gif output, got %q", mimeType)
	}

	animated, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-animated-gif",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"animated.gif"}`),
	})
	if err != nil {
		t.Fatalf("animated gif call: %v", err)
	}
	if !animated.IsError {
		t.Fatalf("expected animated gif to be rejected")
	}
	if got := toolError(t, animated); !strings.Contains(got, "animated GIFs are not supported") {
		t.Fatalf("expected animated GIF guidance, got %q", got)
	}
}

func TestCall_AnimatedWebPRejected(t *testing.T) {
	workspace := t.TempDir()
	imagePath := filepath.Join(workspace, "animated.webp")
	if err := os.WriteFile(imagePath, encodedAnimatedWebP(t), 0o644); err != nil {
		t.Fatalf("write animated webp: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-animated-webp",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"animated.webp"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected animated webp to be rejected")
	}
	if got := toolError(t, result); !strings.Contains(got, "animated WebP images are not supported") {
		t.Fatalf("expected animated WebP guidance, got %q", got)
	}
}

func TestCall_StillWebPAccepted(t *testing.T) {
	workspace := t.TempDir()
	imagePath := filepath.Join(workspace, "still.webp")
	if err := os.WriteFile(imagePath, encodedStillWebP(t), 0o644); err != nil {
		t.Fatalf("write still webp: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-still-webp",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"still.webp"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected still WebP success, got %s", string(result.Output))
	}
	mimeType, _ := decodeSingleImageDataURL(t, result)
	if mimeType != "image/webp" {
		t.Fatalf("expected still WebP output, got %q", mimeType)
	}
}

func TestCall_CorruptImageReturnsToolError(t *testing.T) {
	workspace := t.TempDir()
	imagePath := filepath.Join(workspace, "corrupt.png")
	if err := os.WriteFile(imagePath, make([]byte, 1024), 0o644); err != nil {
		t.Fatalf("write corrupt image: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-corrupt",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"corrupt.png"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool error result for corrupt image")
	}
	if got := toolError(t, result); !strings.Contains(got, "unable to decode image") {
		t.Fatalf("expected decode error, got %q", got)
	}
}

func TestCall_HugeDecodedDimensionsRejected(t *testing.T) {
	workspace := t.TempDir()
	imagePath := filepath.Join(workspace, "huge-dimensions.png")
	if err := os.WriteFile(imagePath, pngWithDimensions(t, 100_000, 100_000), 0o644); err != nil {
		t.Fatalf("write huge-dimension image: %v", err)
	}

	tool, err := New(workspace, true)
	if err != nil {
		t.Fatalf("new tool: %v", err)
	}

	result, err := tool.Call(context.Background(), tools.Call{
		ID:    "call-huge-dimensions",
		Name:  toolspec.ToolViewImage,
		Input: json.RawMessage(`{"path":"huge-dimensions.png"}`),
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected huge decoded dimensions to be rejected")
	}
	if got := toolError(t, result); !strings.Contains(got, "exceed the supported pixel limit") {
		t.Fatalf("expected decoded pixel limit error, got %q", got)
	}
}

func generatedPhotoLikeImage(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x*13 + y*7) % 256),
				G: uint8((x*5 + y*11) % 256),
				B: uint8((x*3 + y*17) % 256),
				A: 255,
			})
		}
	}
	return img
}

func encodedGIF(t *testing.T, frames int) []byte {
	t.Helper()
	palette := []color.Color{color.Black, color.White}
	images := make([]*image.Paletted, 0, frames)
	delays := make([]int, 0, frames)
	for idx := 0; idx < frames; idx++ {
		img := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
		img.SetColorIndex(idx%2, idx%2, 1)
		images = append(images, img)
		delays = append(delays, 0)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: images, Delay: delays}); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func encodedAnimatedWebP(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	encoder := animation.NewEncoder(&buf, 2, 2, &animation.EncodeOptions{Quality: 80, Kmax: 1})
	if encoder == nil {
		t.Fatal("create animated WebP encoder")
	}
	first := image.NewRGBA(image.Rect(0, 0, 2, 2))
	first.Set(0, 0, color.White)
	second := image.NewRGBA(image.Rect(0, 0, 2, 2))
	second.Set(1, 1, color.White)
	if err := encoder.AddFrame(first, 100*time.Millisecond); err != nil {
		t.Fatalf("add first WebP frame: %v", err)
	}
	if err := encoder.AddFrame(second, 100*time.Millisecond); err != nil {
		t.Fatalf("add second WebP frame: %v", err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatalf("close animated WebP encoder: %v", err)
	}
	return buf.Bytes()
}

func encodedStillWebP(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	if err := webp.Encode(&buf, img, webp.OptionsForPreset(webp.PresetPicture, 80)); err != nil {
		t.Fatalf("encode still WebP: %v", err)
	}
	return buf.Bytes()
}

func pngWithDimensions(t *testing.T, width, height uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10})

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], width)
	binary.BigEndian.PutUint32(ihdr[4:8], height)
	ihdr[8] = 8
	ihdr[9] = 2
	writePNGChunk(&buf, "IHDR", ihdr)
	writePNGChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func writePNGChunk(buf *bytes.Buffer, chunkType string, data []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	buf.Write(length[:])
	buf.WriteString(chunkType)
	buf.Write(data)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write([]byte(chunkType))
	_, _ = checksum.Write(data)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], checksum.Sum32())
	buf.Write(crc[:])
}

func decodeSingleImageDataURL(t *testing.T, result tools.Result) (string, []byte) {
	t.Helper()
	var items []map[string]any
	if err := json.Unmarshal(result.Output, &items); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one content item, got %d", len(items))
	}
	if got := items[0]["type"]; got != "input_image" {
		t.Fatalf("expected input_image type, got %#v", got)
	}
	url, ok := items[0]["image_url"].(string)
	if !ok {
		t.Fatalf("expected image_url string, got %#v", items[0]["image_url"])
	}
	if !strings.HasPrefix(url, "data:") {
		t.Fatalf("expected data URL, got %q", url)
	}
	parts := strings.SplitN(strings.TrimPrefix(url, "data:"), ";base64,", 2)
	if len(parts) != 2 {
		t.Fatalf("expected base64 data URL, got %q", url)
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode base64 image: %v", err)
	}
	return parts[0], decoded
}
