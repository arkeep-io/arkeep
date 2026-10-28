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

// validateHookTimeout checks the timeout_secs of a hook definition, which the
// GUI stores as a JSON object. A hook that is not a JSON object, or has no
// timeout_secs, is left alone.
func validateHookTimeout(hook string) error {
	var def map[string]json.RawMessage
	if json.Unmarshal([]byte(hook), &def) != nil {
		return nil
	}
	raw, ok := def["timeout_secs"]
	if !ok {
		return nil
	}
	var secs int
	if err := json.Unmarshal(raw, &secs); err != nil || secs < 0 || secs > maxHookTimeoutSecs {
		return fmt.Errorf("timeout_secs must be a whole number between 0 and %d", maxHookTimeoutSecs)
	}
	return nil
}
