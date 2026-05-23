package readimage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

func countGIFFrames(data []byte, maxFrames int) (int, error) {
	reader := bytes.NewReader(data)
	header := make([]byte, 13)
	if _, err := io.ReadFull(reader, header); err != nil {
		return 0, err
	}
	signature := string(header[:6])
	if signature != "GIF87a" && signature != "GIF89a" {
		return 0, errors.New("invalid GIF header")
	}
	if err := skipGIFColorTable(reader, header[10]); err != nil {
		return 0, err
	}

	frames := 0
	for {
		blockType, err := reader.ReadByte()
		if err != nil {
			return frames, err
		}
		switch blockType {
		case 0x3b:
			return frames, nil
		case 0x21:
			if _, err := reader.ReadByte(); err != nil {
				return frames, err
			}
			if err := skipGIFSubBlocks(reader); err != nil {
				return frames, err
			}
		case 0x2c:
			descriptor := make([]byte, 9)
			if _, err := io.ReadFull(reader, descriptor); err != nil {
				return frames, err
			}
			if err := skipGIFColorTable(reader, descriptor[8]); err != nil {
				return frames, err
			}
			if _, err := reader.ReadByte(); err != nil {
				return frames, err
			}
			if err := skipGIFSubBlocks(reader); err != nil {
				return frames, err
			}
			frames++
			if maxFrames > 0 && frames >= maxFrames {
				return frames, nil
			}
		default:
			return frames, fmt.Errorf("unknown GIF block type 0x%02x", blockType)
		}
	}
}

func skipGIFColorTable(reader *bytes.Reader, packed byte) error {
	if packed&0x80 == 0 {
		return nil
	}
	colorTableSize := 3 * (1 << ((packed & 0x07) + 1))
	return skipBytes(reader, int64(colorTableSize))
}

func skipGIFSubBlocks(reader *bytes.Reader) error {
	for {
		size, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if size == 0 {
			return nil
		}
		if err := skipBytes(reader, int64(size)); err != nil {
			return err
		}
	}
}

func skipBytes(reader *bytes.Reader, n int64) error {
	if n < 0 || int64(reader.Len()) < n {
		return io.ErrUnexpectedEOF
	}
	_, err := reader.Seek(n, io.SeekCurrent)
	return err
}
