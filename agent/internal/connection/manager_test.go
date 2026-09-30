package connection

import (
	"bytes"
	"errors"
	"testing"

	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestChunkWriter checks that writes are split into messages no larger than
// downloadChunkSize, reassemble to the original bytes, and that a send error
// stops the write.
func TestChunkWriter(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 2*downloadChunkSize+10)

	t.Run("splits and preserves data", func(t *testing.T) {
		var got bytes.Buffer
		var sizes []int
		w := chunkWriter(func(c *proto.SnapshotDownloadChunk) error {
			sizes = append(sizes, len(c.Data))
			got.Write(c.Data)
			return nil
		})
		n, err := w.Write(data)
		if err != nil || n != len(data) {
			t.Fatalf("Write() = %d, %v; want %d, nil", n, err, len(data))
		}
		if !bytes.Equal(got.Bytes(), data) {
			t.Error("reassembled chunks differ from the written data")
		}
		for _, s := range sizes {
			if s > downloadChunkSize {
				t.Errorf("chunk of %d bytes exceeds downloadChunkSize", s)
			}
		}
	})

	t.Run("stops on send error", func(t *testing.T) {
		sendErr := errors.New("stream closed")
		calls := 0
		w := chunkWriter(func(*proto.SnapshotDownloadChunk) error {
			calls++
			if calls == 2 {
				return sendErr
			}
			return nil
		})
		n, err := w.Write(data)
		if !errors.Is(err, sendErr) || n != downloadChunkSize {
			t.Errorf("Write() = %d, %v; want %d, %v", n, err, downloadChunkSize, sendErr)
		}
	})
}
