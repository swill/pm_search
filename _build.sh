#!/usr/bin/env bash

esc -o static.go static
gox -output="bin/{{.Dir}}_{{.OS}}_{{.Arch}}"