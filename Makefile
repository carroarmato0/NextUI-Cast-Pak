.PHONY: test build-native build-tg5040 build-tg5050 build-my355 build-all release deploy clean build-cedar-probe build-cedar-probe-tg5040 build-cedar-probe-tg5050 build-cedar-probe-my355

test:
	./scripts/test.sh

build-native:
	./scripts/build.sh native

build-tg5040:
	./scripts/build.sh tg5040

build-tg5050:
	./scripts/build.sh tg5050

build-my355:
	./scripts/build.sh my355

build-all:
	./scripts/build.sh all

release:
	./scripts/release.sh

deploy:
	./scripts/deploy.sh

clean:
	rm -rf bin/native bin/tg5040/cast bin/tg5050/cast bin/my355/cast dist/ .go_cache/

_CEDAR_RT := $(or $(CONTAINER_RUNTIME),$(shell command -v podman >/dev/null 2>&1 && echo podman || echo docker))

build-cedar-probe: build-cedar-probe-tg5040 build-cedar-probe-tg5050 build-cedar-probe-my355

build-cedar-probe-tg5040:
	mkdir -p bin/tg5040
	$(_CEDAR_RT) run --rm -v "$(CURDIR):/workspace" -w /workspace -e IN_CONTAINER=1 cast-pak-tg5040-dev sh -c '$$CC -O2 -Wall -o bin/tg5040/cedar-probe cmd/cedar-probe/cedar-probe.c -ldl'

build-cedar-probe-tg5050:
	mkdir -p bin/tg5050
	$(_CEDAR_RT) run --rm -v "$(CURDIR):/workspace" -w /workspace -e IN_CONTAINER=1 cast-pak-tg5050-dev sh -c '$$CC -O2 -Wall -o bin/tg5050/cedar-probe cmd/cedar-probe/cedar-probe.c -ldl'

build-cedar-probe-my355:
	mkdir -p bin/my355
	$(_CEDAR_RT) run --rm -v "$(CURDIR):/workspace" -w /workspace -e IN_CONTAINER=1 cast-pak-my355-dev sh -c '$$CC -O2 -Wall -o bin/my355/cedar-probe cmd/cedar-probe/cedar-probe.c -ldl'
