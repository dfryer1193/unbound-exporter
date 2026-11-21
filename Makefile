# This Makefile is yours: Ivaldi wrote it once and won't touch it again.
# The shared targets (lint, test, build, run-<name>, install and setup-hooks)
# come from ivaldi.mk. Add your own targets below.

include ivaldi.mk

MIMIR_VERSION := v0.1.2

.PHONY: generate
# Regenerate service/exporter_config_gen.go from service/exporter_config.json.
# mimir-generate puts its "Code generated" header right above the package clause,
# where Go reads it as a second package doc (godoclint fails); the sed separates them.
generate:
	go run github.com/worldtree-industries/mimir/cmd/mimir-generate@$(MIMIR_VERSION)
	sed -i '1{/^\/\/ Code generated/G}' service/exporter_config_gen.go
