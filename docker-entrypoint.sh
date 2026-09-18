#!/bin/sh
set -eu

data_dir="${MAILTAIL_DATA_DIR:-/data}"

if [ "$(id -u)" = "0" ]; then
	case "$data_dir" in
	"" | /)
		echo "mailtail: refusing to change ownership of an unsafe data directory: '$data_dir'" >&2
		exit 1
		;;
	esac

	mkdir -p "$data_dir"
	chown -R mailtail:mailtail "$data_dir"
	exec su-exec mailtail:mailtail "$@"
fi

exec "$@"
