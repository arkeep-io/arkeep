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

The agent asks Docker where it keeps volumes (`DockerRootDir` + `/volumes`),
so a custom `data-root` is protected the same way. The volumes of running
containers are excluded even when that directory is not visible to the agent.

Cleanup:

```bash
docker rm -f smoke-running; docker volume rm smokevol
rm -rf /tmp/arkeep-smoke
```

## 6. Running container bind mounts are skipped

```bash
mkdir -p /tmp/arkeep-smoke/bind
echo original > /tmp/arkeep-smoke/bind/marker.txt
docker run -d --name smoke-bind -v /tmp/arkeep-smoke/bind:/data "$BUSYBOX" sleep 3600
```

1. GUI: back up a **path** source `/tmp/arkeep-smoke/bind`.
2. `echo damaged > /tmp/arkeep-smoke/bind/marker.txt`
3. GUI: restore in place.
4. Expected: the job log contains
   `skipping bind mount "/tmp/arkeep-smoke/bind" for in-place restore: container "smoke-bind" is running`,
   and `marker.txt` still reads `damaged`. Only writable bind mounts are
   protected (`:ro` ones are restored normally). Not protected, by design:
   the agent's own bind mounts, binds of exactly the Docker volumes root or
   Docker's data root (running containers' volumes are excluded one by one),
   sockets such as `docker.sock`, and `/` or a drive root. A bind of any
   folder above the data root (e.g. `/mnt/storage` with data root
   `/mnt/storage/docker`) is protected like any other.
5. A targeted in-place restore fails with `cannot restore … in place: it
   contains …` if a selected folder (e.g. `/tmp/arkeep-smoke`) contains a
   running container's bind mount or volume, even when that path is not in
   the snapshot: restic does not accept `--include` and `--exclude` together.
   Select more specific paths, stop the container, or restore to a folder.

Docker Desktop reports bind sources on Windows drives as
`/run/desktop/mnt/host/c/…` or `/host_mnt/c/…`; the agent maps them to
`/hostfs/c/…`. A native Windows agent keeps the existing limitation: its
snapshot paths (`/C/…`) are not matched against the excluded paths.

Cleanup: `docker rm -f smoke-bind`.

## 7. Without the Docker socket, volumes are skipped

Remove the `/var/run/docker.sock` mount and restart the agent. Restore in
place a snapshot that contains a Docker volume (step 5).

Expected: the volume is **not** restored, everything else is, and the log
warns: `cannot check which containers are running (Docker was not reachable
when the agent started), so Docker volumes under … were skipped by the
in-place restore`.
The same happens if Docker cannot list the containers. To restore volumes in
this setup, mount the socket, or stop the containers, restore to a folder and
copy the files back.

Without the socket the agent cannot see bind mounts either, so running
containers' bind mounts (step 6) are only protected when the socket is
mounted.

## 8. Gated real-Docker Go test (root)

```bash
cd agent/internal/executor && go test -c -o /tmp/executor.test . && sudo /tmp/executor.test -test.run 'TestInPlace_RealDocker' -test.v -test.count=1 -test.timeout=10m; rm -f /tmp/executor.test
```
