package executor

import (
	"os"
	"testing"

	"github.com/arkeep-io/arkeep/agent/internal/restic/restictest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	restictest.Cleanup()
	os.Exit(code)
}
