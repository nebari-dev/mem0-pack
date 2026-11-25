# Local development Makefile for the mem0 pack.
#
# Targets drive the kind end-to-end harness in tests/e2e/kind-e2e.sh
# (the targets below call its up, test and down commands).
#
# Prerequisites: docker, kind, helm, kubectl
#
# Usage:
#   make kind-up   Create the kind cluster and its dependencies (the pack is installed by run.sh install)
#   make e2e       Run the end-to-end checks against the cluster
#   make down      Delete the kind cluster

E2E := ../tests/e2e/kind-e2e.sh

.PHONY: kind-up e2e down

kind-up:
	bash $(E2E) up

e2e:
	bash $(E2E) test

down:
	bash $(E2E) down
