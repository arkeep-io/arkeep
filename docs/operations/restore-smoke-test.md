# Arkeep — Restore Smoke Test

Manual checklist to verify restores end to end with the published Docker agent.
Run it after changes to the restore path. Use a throwaway host.

---

## 1. Start the agent with writable `/hostfs`

Use the mounts from `deploy/docker/docker-compose.agent.yml`, with one change:
the default `/:/hostfs:ro` blocks in-place restore, so mount it read-write.

```yaml
volumes:
  - /:/hostfs:rw
  - /var/lib/docker/volumes:/var/lib/docker/volumes:ro
  - /var/run/docker.sock:/var/run/docker.sock
```

Connect the agent to the server and create a **local** destination
(e.g. `/tmp/arkeep-smoke/repo`).

## 2. Create test data

```bash
mkdir -p /tmp/arkeep-smoke/data/sub
for i in 1 2 3; do head -c 100000 /dev/urandom > /tmp/arkeep-smoke/data/file$i.bin; done
echo hello > /tmp/arkeep-smoke/data/sub/hello.txt
cp -a /tmp/arkeep-smoke/data /tmp/arkeep-smoke/expected
```

In the GUI: create a policy with source type **path**
`/tmp/arkeep-smoke/data`, the local destination, and run it. Wait for
status `success`.

## 3. Restore in place

```bash
rm /tmp/arkeep-smoke/data/file2.bin
echo damaged > /tmp/arkeep-smoke/data/sub/hello.txt
touch /tmp/arkeep-smoke/data/stray.txt
```

GUI: restore the snapshot, target **original location**. Then:

```bash
diff -r /tmp/arkeep-smoke/expected /tmp/arkeep-smoke/data
```

Expected: `file2.bin` and `hello.txt` are restored. `stray.txt` remains
(restore does not delete extra files), so `diff` reports only
`Only in .../data: stray.txt`.

## 4. Restore to a folder

GUI: restore the same snapshot to the folder `/tmp/arkeep-smoke/restore`.

With the Docker agent, restic recreates the snapshot's absolute path under
the target and the host is mounted at `/hostfs`, so files land under
`<target>/hostfs/<original path>`:

```bash
diff -r /tmp/arkeep-smoke/expected \
  /tmp/arkeep-smoke/restore/hostfs/tmp/arkeep-smoke/data
```

Expected: no differences.

## 5. Running container volumes are skipped

Regression check: with `/:/hostfs` writable, an in-place restore of a snapshot
taken from a `/` or `/var/lib` path source used to overwrite the volumes of
running containers, because the excludes only covered `/var/lib/docker/volumes`
and not `/hostfs/var/lib/docker/volumes`. Both are now excluded, with warnings.

```bash
# busybox 1.37.0, pinned by digest (ADR 0013), as in restore_real_docker_e2e_test.go
BUSYBOX=busybox@sha256:a7dbc319b1438115a207e11930dc4bb3e643dcb3ec0bdd694bb36cd8fad289ec
docker volume create smokevol
docker run -d --name smoke-running -v smokevol:/data "$BUSYBOX" \
  sh -c 'echo original > /data/marker.txt; sleep 3600'
VOL=$(docker volume inspect smokevol -f '{{.Mountpoint}}')
echo "$VOL"   # /var/lib/docker/volumes/smokevol/_data
```

1. GUI: create a policy with a **path** source `$VOL` (the path printed above)
   and run a backup.
2. Damage the file:
   ```bash
   sudo sh -c "echo damaged > $VOL/marker.txt"
   ```
3. GUI: restore in place.
4. Expected: the job log contains a warning that the volume **is running**
   and the path is skipped; `marker.txt` still reads `damaged`:
   ```bash
   sudo cat "$VOL/marker.txt"
   ```
5. Optional: `docker stop smoke-running`, restore in place again, and
   confirm `marker.txt` is now `original`.

Cleanup:

```bash
docker rm -f smoke-running; docker volume rm smokevol
rm -rf /tmp/arkeep-smoke
```

## 6. Gated real-Docker Go test (root)

```bash
cd agent/internal/executor && go test -c -o /tmp/executor.test . && sudo /tmp/executor.test -test.run 'TestInPlace_RealDocker' -test.v -test.count=1 -test.timeout=10m; rm -f /tmp/executor.test
```
