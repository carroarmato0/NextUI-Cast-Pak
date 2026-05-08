.PHONY: test build-native build-tg5040 build-tg5050 build-my355 build-all release deploy clean

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
