#!/bin/sh
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/walite-notifier-parser.XXXXXX")
trap 'rm -rf -- "$test_dir"' 0
sh -n "$script_dir/build.sh" "$script_dir/post-test-request.sh"
xcrun swiftc "$script_dir/NotificationRequest.swift" "$script_dir/tests/RequestTests.swift" \
	-o "$test_dir/request-tests"
"$test_dir/request-tests"
