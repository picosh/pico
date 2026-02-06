#!/bin/bash

podman build --build-arg "APP=$*" --target release-web .
