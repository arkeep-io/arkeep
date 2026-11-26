package restic

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"

	"github.com/arkeep-io/arkeep/shared/repair"
)

// maxRepairLine caps one line of restic output handed to the job log: restic
// prints short lines, and a runaway one must not become a huge log entry.
const maxRepairLine = 4096

// BuildRepairArgs validates p and returns the restic arguments that run it.
// Values that come from the user are appended after a "--" end-of-options
// marker where they are positional, and as separate arguments after their
// flag otherwise: nothing is ever joined into a string a shell would parse.
func BuildRepairArgs(p repair.Params) ([]string, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	switch p.Action {
	case repair.Unlock:
		return []string{"unlock"}, nil
	case repair.Index:
		args := []string{"repair", "index"}
		if p.ReadAllPacks {
			args = append(args, "--read-all-packs")
		}
		return args, nil
	case repair.Snapshots:
		args := []string{"repair", "snapshots"}
		if !p.Apply {
			args = append(args, "--dry-run")
		}
		if p.Forget {
			args = append(args, "--forget")
		}
		for _, h := range p.Hosts {
			args = append(args, "--host", h)
		}
		for _, t := range p.Tags {
			args = append(args, "--tag", t)
		}
		args = append(args, "--")
		return append(args, p.SnapshotIDs...), nil
	case repair.Packs:
		return append([]string{"repair", "packs", "--"}, p.PackIDs...), nil
	case repair.FindPacks:
		return append([]string{"find", "--pack", "--"}, p.PackIDs...), nil
	case repair.Check:
		args := []string{"check"}
		if p.ReadData {
			args = append(args, "--read-data")
		}
		return args, nil
	}
	// Validate accepted an action this switch does not know: a programming
	// error, refused rather than run as something else.
	return nil, fmt.Errorf("restic: no command for repair action %q", p.Action)
}

// Repair runs the repair request p against dest and calls onLine with each
// line restic writes, stdout and stderr together, while it runs. The
// repository password and backend credentials reach restic through its
// environment only (buildCmd), never its arguments.
//
// A lock held by another operation is reported, not removed: the admin
// decides whether to run the unlock action.
func (w *Wrapper) Repair(ctx context.Context, dest Destination, p repair.Params, onLine func(string)) error {
	args, err := BuildRepairArgs(p)
	if err != nil {
		return err
	}
	return w.runStreaming(ctx, dest, args, onLine)
}

// runStreaming runs restic with args and hands each line of its combined
// output to onLine as it arrives. The returned error wraps the
// *exec.ExitError of a failed run, so callers can read the exit code with
// ExitCode.
func (w *Wrapper) runStreaming(ctx context.Context, dest Destination, args []string, onLine func(string)) error {
	cmd := w.buildCmd(ctx, dest, args)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > maxRepairLine {
				line = line[:maxRepairLine] + " …"
			}
			if onLine != nil {
				onLine(line)
			}
		}
		// A line longer than the scanner's buffer stops it: keep draining so
		// restic never blocks on a full pipe.
		_, _ = io.Copy(io.Discard, pr)
	}()

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		<-done
		return fmt.Errorf("restic: failed to start: %w", err)
	}
	err := cmd.Wait()
	_ = pw.Close()
	<-done
	if err != nil {
		return fmt.Errorf("restic %s failed: %w", args[0], err)
	}
	return nil
}

// ExitCode returns restic's exit status from an error returned by Repair,
// or -1 when restic did not exit on its own (it did not start, or was
// killed).
func ExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
