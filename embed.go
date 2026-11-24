package main

import "embed"

//go:embed all:frontend/dist
var assets embed.FS

//go:embed worker/worker.py
var workerScript []byte

//go:embed worker/params.py
var canonicalParamsPy []byte

//go:embed examples/demo_project/components/*.py examples/demo_project/models/*/*.py examples/demo_project/models/*/*.json examples/demo_project/utils/*.py
var demoProjectFS embed.FS
