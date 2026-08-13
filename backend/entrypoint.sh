#!/bin/sh
# The data directories are bind mounts from the host, so their owner is whatever
# the host says — usually not the container's user. Fix that once, then drop
# privileges and run the server as `app`.
#
# The recursive pass only happens when the top-level directory is not already
# ours, so a restart with a large /srv/git costs nothing.
set -e

for dir in /srv/git /srv/data; do
	mkdir -p "$dir"
	owner=$(stat -c '%u' "$dir" 2>/dev/null || echo 0)
	if [ "$owner" != "$(id -u app)" ]; then
		echo "entrypoint: taking ownership of $dir"
		chown -R app:app "$dir" || echo "entrypoint: could not chown $dir — continuing"
	fi
done

exec su-exec app "$@"
