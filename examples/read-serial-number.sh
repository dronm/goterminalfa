#!/bin/sh
set -eu

# Run from the project root. Add an Authorization header if configured.
curl --fail-with-body --silent --show-error \
	-H 'Content-Type: application/json' \
	--data-binary @examples/read-serial-number.json \
	http://127.0.0.1:8080/api/v1/commands
