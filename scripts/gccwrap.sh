#!/bin/bash

if [[ "$GOARCH" == "arm64" ]]; then
	GCC_ARCH="aarch64"
elif [[ "$GOARCH" == "amd64" ]]; then
	GCC_ARCH="x86_64"
fi

exec "${GCC_ARCH}-linux-gnu-gcc" "$@"
