#!/bin/sh
# Copy the working tree to a Docker host and run a command there, for
# development machines without Docker. Not needed if you have Docker locally.
#
#   REMOTE=user@dockerhost sh tools/remote.sh sh tools/verify.sh
#
# Uses ~/build/youless-tig on the remote host (REMOTE_DIR to change). Files are
# copied, not synced back: fetch generated files you want to keep explicitly.
set -eu
: "${REMOTE:?set REMOTE=user@host}"
DIR="${REMOTE_DIR:-build/youless-tig}"
SSH_OPTS="${SSH_OPTS:-}"
cd "$(dirname "$0")/.."
tar --exclude=./.git --exclude=./.env -cf - . |
  ssh $SSH_OPTS "$REMOTE" "rm -rf '$DIR' && mkdir -p '$DIR' && tar -xf - -C '$DIR'"
ssh $SSH_OPTS "$REMOTE" "cd '$DIR' && $*"
