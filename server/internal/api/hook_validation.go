package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const hookMaxLen = 1024

// maxHookTimeoutSecs caps a hook's timeout_secs. 0 uses the agent's default
// hook timeout. The GUI enforces the same range.
const maxHookTimeoutSecs = 24 * 60 * 60

// validateHookCommand validates a hook shell command before it is persisted.
// Hooks are executed on the agent via /bin/sh -c "<command>" (or cmd /C on
// Windows), so the server validates them at save time to block the most
// critical injection patterns without preventing legitimate use.
//
// Blocked patterns:
//   - Sensitive environment variable references ($RESTIC_*, $RCLONE_*, $ARKEEP_*)
//     which would expose encrypted repository passwords or cloud credentials
//     to the hook process.
//   - Command substitution ($(...) and backticks), which can capture and
//     exfiltrate the values of those environment variables.
//   - Path traversal sequences (..) to prevent hooks from referencing files
//     outside their intended directories via relative paths.
//
// Standard shell operators (|, >, &&, ;) are intentionally allowed because
// they are required for common patterns such as:
//
//	pg_dump mydb | gzip > /backup/dump.sql.gz
//	mysqldump -u root mydb > /var/backups/mysql.sql
//
// An empty command is always valid (hooks are optional).
func validateHookCommand(cmd string) error {
	if cmd == "" {
		return nil
	}
	if len(cmd) > hookMaxLen {
		return errors.New("hook command must not exceed 1024 characters")
	}
	// Command substitution — $(...) or backtick form.
	if strings.Contains(cmd, "$(") || strings.Contains(cmd, "`") {
		return errors.New("hook command must not use command substitution ($(...) or backticks)")
	}
	// Path traversal.
	if strings.Contains(cmd, "..") {
		return errors.New("hook command must not contain path traversal sequences (..)")
	}
	// Sensitive environment variable references.
	// Checked against the uppercase form to match both $RESTIC_PASSWORD and
	// the unlikely but possible ${restic_password} variant.
	upper := strings.ToUpper(cmd)
	for _, prefix := range []string{"$RESTIC_", "${RESTIC_", "$RCLONE_", "${RCLONE_", "$ARKEEP_", "${ARKEEP_"} {
		if strings.Contains(upper, prefix) {
			return errors.New("hook command must not reference internal environment variables ($RESTIC_*, $RCLONE_*, $ARKEEP_*)")
		}
	}
	return nil
}

// hookDefinition is a hook as the GUI saves it and the agent runs it
// (hookPayload in agent/internal/executor). Decoding into the same fields with
// encoding/json, as the agent does, makes the server check exactly the
// command and arguments the agent will execute.
type hookDefinition struct {
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	TimeoutSecs int      `json:"timeout_secs"`
}

// validateHook validates a hook definition before it is persisted. An empty
// hook is valid (hooks are optional); anything else must be the JSON object
// the agent expects, or every backup of the policy would fail. The command
// and each argument go through validateHookCommand after decoding: checked on
// the raw JSON text, an escape such as $( would hide a $( from it.
func validateHook(hook string) error {
	if hook == "" {
		return nil
	}
	var def hookDefinition
	if err := json.Unmarshal([]byte(hook), &def); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field == "timeout_secs" {
			return fmt.Errorf("timeout_secs must be a whole number between 0 and %d", maxHookTimeoutSecs)
		}
		return errors.New(`hook must be a JSON object such as {"command": "…", "args": [], "timeout_secs": 30}`)
	}
	if def.TimeoutSecs < 0 || def.TimeoutSecs > maxHookTimeoutSecs {
		return fmt.Errorf("timeout_secs must be a whole number between 0 and %d", maxHookTimeoutSecs)
	}
	if err := validateHookCommand(def.Command); err != nil {
		return err
	}
	// Arguments reach the shell too: cmd /C on Windows joins them into the
	// command line.
	for _, arg := range def.Args {
		if err := validateHookCommand(arg); err != nil {
			return fmt.Errorf("hook argument: %w", err)
		}
	}
	return nil
}
