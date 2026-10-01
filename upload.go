package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"

	"github.com/ofabiodev/osmose/proto/media"
	"github.com/ofabiodev/osmose/types"
)

// chunk size for media.uploadFilePart.
const uploadPartSize = 512 * 1024

// sends data to Osmium in parts and returns a reference that can be
// attached to a message with types.UploadedMedia.
func (b *bot) uploadFile(ctx context.Context, name string, data []byte) (*types.UploadedFileRef, error) {
	var idBytes [8]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return nil, err
	}
	uploadID := binary.LittleEndian.Uint64(idBytes[:])

	var part uint32
	for offset := 0; offset < len(data); offset += uploadPartSize {
		end := min(offset+uploadPartSize, len(data))
		_, err := b.client.Raw().Call(ctx, &media.UploadFilePart{
			UploadId: uploadID,
			Part:     part,
			Data:     data[offset:end],
		})
		if err != nil {
			return nil, fmt.Errorf("upload part %d: %w", part, err)
		}
		part++
	}
	return types.UploadedFile(types.ID(uploadID), name, part), nil
}
