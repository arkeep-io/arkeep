package connection

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/arkeep-io/arkeep/agent/internal/restic"
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

// TestProtoToJob checks which assignment types reach the executor. A type
// rejected here is never executed, so the executor-supported FORGET must pass
// (issue #283: a rejected retention job held its destination's busy gate).
func TestProtoToJob(t *testing.T) {
	tests := []struct {
		name    string
		jobID   string
		jobType proto.JobType
		wantErr bool
	}{
		{"backup", "job-1", proto.JobType_JOB_TYPE_BACKUP, false},
		{"restore", "job-1", proto.JobType_JOB_TYPE_RESTORE, false},
		{"forget", "job-1", proto.JobType_JOB_TYPE_FORGET, false},
		{"unsupported type", "job-1", proto.JobType_JOB_TYPE_UNSPECIFIED, true},
		{"missing job id", "", proto.JobType_JOB_TYPE_BACKUP, true},
	}
	m := &Manager{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job, err := m.protoToJob(&proto.JobAssignment{JobId: tt.jobID, Type: tt.jobType})
			if (err != nil) != tt.wantErr {
				t.Fatalf("protoToJob() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && job.Type != tt.jobType {
				t.Errorf("job.Type = %v, want %v", job.Type, tt.jobType)
			}
		})
	}
}

// TestSnapshotImportPayloadDestination is a regression test for import and
// sync failing with "an empty password is not allowed": the password used to
// travel as Env["RESTIC_PASSWORD"], which the env allowlist drops.
func TestSnapshotImportPayloadDestination(t *testing.T) {
	var p snapshotImportPayload
	body := `{"type":"s3","repo_url":"s3:host/bucket","repo_password":"secret","env":{"AWS_ACCESS_KEY_ID":"key"}}`
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	d := p.destination("s3:host/bucket")
	if d.Password != "secret" {
		t.Errorf("Password = %q, want %q", d.Password, "secret")
	}
	if d.RepoURL != "s3:host/bucket" || d.Type != restic.DestS3 || d.Env["AWS_ACCESS_KEY_ID"] != "key" {
		t.Errorf("destination = %+v, want the payload's type, repo URL and env", d)
	}
}
